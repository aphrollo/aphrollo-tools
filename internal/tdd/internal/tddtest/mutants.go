package tddtest

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// MeasuredCall is one invocation the runner's exec seam received. Log is the
// writer the real tool's stdout and stderr go to, so a stand-in can produce
// the OUTPUT a run is judged on — a shard that died of the box is recognised
// from what it printed, not from its exit code alone.
type MeasuredCall struct {
	Dir  string
	Env  []string
	Argv []string
	Log  io.Writer
}

// MutantsExec is the shape of the runner's exec seam.
type MutantsExec = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error)

// StubMutantsExec replaces the runner's exec seam (the variable at exec) for
// one test and records every call. reply is asked what that call should do —
// its exit code, and whatever outcomes file it wants to leave behind. It is
// handed the run's own context as well, so a test can stand in for a child
// that ends when, and only when, its caller gives up.
//
// The mutant-count probe is stood down with the tool it would ask, through
// pinListCount: a stand-in cannot answer a `--list --json`, and a probe that
// cannot answer changes no shard count. A test ABOUT the cap pins the probe
// itself.
//
// A sharded measurement calls the seam from N goroutines at once, so the
// record is taken under a lock: without one the recorder is a data race, and
// a test that reads the calls back is reading whatever survived it. reply
// itself runs unlocked, because a shard's stand-in has to be able to block
// until its context ends while the others run.
func StubMutantsExec(t *testing.T, exec *MutantsExec, pinListCount func(n int, ok bool) (restore func()), reply func(ctx context.Context, n int, c MeasuredCall) (int, error)) *[]MeasuredCall {
	t.Helper()
	t.Cleanup(pinListCount(0, false))
	prev := *exec
	calls := &[]MeasuredCall{}
	var mu sync.Mutex
	*exec = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		mu.Lock()
		*calls = append(*calls, MeasuredCall{Dir: dir, Env: env, Argv: argv, Log: log})
		n, call := len(*calls), (*calls)[len(*calls)-1]
		mu.Unlock()
		if reply == nil {
			return 0, nil
		}
		return reply(ctx, n, call)
	}
	t.Cleanup(func() { *exec = prev })
	return calls
}

// Outcome is what an outcomes file records about one mutant.
type Outcome struct {
	Name, Package, File string
	Line, Col           int
	// Status is the producer's word: caught, missed, unviable or timeout.
	Status string
}

// WriteOutcomes puts a cargo-mutants outcomes file at path, from the mutants
// it names; fields reads one of them.
func WriteOutcomes[M any](t *testing.T, path string, fields func(M) Outcome, mutants ...M) {
	t.Helper()
	type span struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	}
	type mutant struct {
		Name    string `json:"name"`
		Package string `json:"package"`
		File    string `json:"file"`
		Span    span   `json:"span"`
	}
	type scenario struct {
		Mutant mutant `json:"Mutant"`
	}
	type outcome struct {
		Scenario scenario `json:"scenario"`
		Summary  string   `json:"summary"`
	}
	summary := map[string]string{
		"caught": "CaughtMutant", "missed": "MissedMutant",
		"unviable": "Unviable", "timeout": "Timeout",
	}
	doc := struct {
		Outcomes []outcome `json:"outcomes"`
	}{}
	for _, mm := range mutants {
		m := fields(mm)
		var o outcome
		o.Summary = summary[m.Status]
		o.Scenario.Mutant = mutant{Name: m.Name, Package: m.Package, File: m.File}
		o.Scenario.Mutant.Span.Start.Line = m.Line
		o.Scenario.Mutant.Span.Start.Column = m.Col
		doc.Outcomes = append(doc.Outcomes, o)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	MustWrite(t, path, string(data))
}

// Phases is what FakePhases needs from the package's detached-phase code.
type Phases[J, O any] struct {
	// Spawn is the detached-phase spawner variable.
	Spawn *func(J) (J, bool)
	// Start stamps pid and started on j, saves the job and returns it as
	// read back from disk.
	Start func(j J, pid int, started time.Time) J
	// Files are the log and result paths the phase writes.
	Files func(j J) (log, result string)
	// WriteResult writes a phase outcome to path.
	WriteResult func(path string, out O)
	// Enable turns deferred phases on or off.
	Enable func(on bool)
}

// FakePhases replaces the detached-phase spawner for a test: each spawn is
// recorded, and finishes immediately with the queued outcome (or never, when
// the queue says so). Returns the recorded spawns.
func FakePhases[J, O any](t *testing.T, p Phases[J, O], outcomes ...*O) *[]J {
	t.Helper()
	var spawned []J
	i := 0
	prev := *p.Spawn
	*p.Spawn = func(j J) (J, bool) {
		j = p.Start(j, 1000+i, time.Now())
		spawned = append(spawned, j)
		var out *O
		if i < len(outcomes) {
			out = outcomes[i]
		}
		i++
		if out != nil {
			log, result := p.Files(j)
			if err := os.WriteFile(log, []byte("test result: ok. 1 passed; 0 failed"), 0o600); err != nil {
				t.Error(err)
			}
			p.WriteResult(result, *out)
		}
		return j, true
	}
	t.Cleanup(func() { *p.Spawn = prev })
	p.Enable(true)
	t.Cleanup(func() { p.Enable(false) })
	return &spawned
}
