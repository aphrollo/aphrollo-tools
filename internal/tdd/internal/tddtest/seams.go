package tddtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Swap sets the package variable at p to v for the rest of the test.
func Swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}

// Replace sets the package variable at p to v and returns the undo.
func Replace[T any](t *testing.T, p *T, v T) (restore func()) {
	t.Helper()
	prev := *p
	*p = v
	return func() { *p = prev }
}

// IsolateBuildLock points the build lock at a per-test lock file for the
// test's duration, instead of the real machine-wide one, through setPath (the
// package's lock-path override). Without this, a test exercising the lock
// races the box's OWN aphrollo PostToolUse hook (which runs `go test` against
// this working tree after every Edit/Write, and once installed exercises this
// exact production lock file too) — spurious contention unrelated to the
// behavior under test.
//
// It also shrinks the PostEdit/Precommit lock-wait deadlines from their
// production values (20s / 300s) to a couple hundred milliseconds: a
// contention test needs the DEADLINE to actually elapse to exercise the
// "gave up waiting" path, and the project's test-quality bar forbids a real
// sleep over 200ms — waiting out a real 20s or 300s budget just to prove a
// timeout path works would violate that outright (and was observed to,
// costing 320s for two tests before this fix).
func IsolateBuildLock(t *testing.T, setPath func(path string) (restore func()), setPostEdit, setPrecommit func(time.Duration) (restore func())) {
	t.Helper()
	restorePath := setPath(filepath.Join(t.TempDir(), "test-build.lock"))
	restorePostEdit := setPostEdit(120 * time.Millisecond)
	restorePrecommit := setPrecommit(150 * time.Millisecond)
	t.Cleanup(func() {
		restorePath()
		restorePostEdit()
		restorePrecommit()
	})
}

// Decide runs decidePreEdit over payload and fails the test on an error.
func Decide[D any](t *testing.T, payload string, decidePreEdit func(raw []byte) (D, error)) D {
	t.Helper()
	d, err := decidePreEdit([]byte(payload))
	if err != nil {
		t.Fatalf("DecidePreEdit(%s): %v", payload, err)
	}
	return d
}

// DecideBash runs decideBashSuite over a Bash payload and fails the test if
// the payload was not judged at all — every caller names a command
// DecideBashSuite must recognise as a test-runner invocation.
func DecideBash[D any](t *testing.T, session, cwd, command string, decideBashSuite func(raw []byte) (D, bool)) D {
	t.Helper()
	d, judged := decideBashSuite(BashPayload(t, session, cwd, command))
	if !judged {
		t.Fatalf("DecideBashSuite did not judge %q as a suite invocation", command)
	}
	return d
}

// FakeRun is a suite runner that ignores its inputs and yields result, so a
// gate can be exercised without spawning a real test suite.
func FakeRun[R, S any](result S) func(R, string) S {
	return func(R, string) S { return result }
}

// RecordRunner is a suite runner that records every runner it executes at
// root and always reports pass. keep decides what is recorded, and in what
// form; nil records every runner as it arrived.
func RecordRunner[R, S any](seen *[]R, root string, keep func(r R) (R, bool), pass S) func(R, string) S {
	return func(r R, dir string) S {
		if dir != root {
			return pass
		}
		if keep == nil {
			*seen = append(*seen, r)
			return pass
		}
		if kept, ok := keep(r); ok {
			*seen = append(*seen, kept)
		}
		return pass
	}
}

// NoteAfterGate runs one gate over root, lets the commit path stamp whatever
// it proved, makes the commit and returns the gate note under notesRef the
// post-commit hook wrote for it — "" when it wrote none. That note is the
// gate's claim as a machine reads it.
func NoteAfterGate(t *testing.T, root, notesRef string, gate func() (blocked bool, message string), stamp, postCommit func(root string)) string {
	t.Helper()
	if blocked, message := gate(); blocked {
		t.Fatalf("unexpected block: %s", message)
	}
	stamp(root)
	GitDo(t, root, "commit", "-qm", "Widen alpha")
	postCommit(root)
	return GitNote(t, notesRef, root, "HEAD")
}

// CommitWithGreenGateNote commits a tree a suite proved green, carrying the
// note the post-commit hook would have written: stamp records the proof,
// postCommit writes the note.
func CommitWithGreenGateNote(t *testing.T, root, subject string, stamp, postCommit func(root string)) {
	t.Helper()
	Write(t, root, "landed.go", "package m\n")
	GitDo(t, root, "add", ".")
	stamp(root)
	GitDo(t, root, "commit", "-q", "-m", subject)
	postCommit(root)
}

// GateLineFields is the package's gate.log line parser, reduced to what the
// verdict helpers read.
type GateLineFields func(line string) (stage, verdict string, ok bool)

// PostEditVerdicts is every verdict gate.log holds for the postedit stage, in
// order — what the gate decided a run MEANT, read from its own durable record
// rather than from the wording of an advisory.
func PostEditVerdicts(t *testing.T, cfg string, parse GateLineFields) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(GateLogText(t, cfg), "\n") {
		if stage, verdict, ok := parse(line); ok && stage == "postedit" {
			out = append(out, verdict)
		}
	}
	return out
}

// RequireLoggedVerdict fails unless gate.log carries a PARSEABLE line with
// this verdict — a line stats cannot read is a line nobody counts.
func RequireLoggedVerdict(t *testing.T, cfg, verdict string, parse GateLineFields) {
	t.Helper()
	text := GateLogText(t, cfg)
	for line := range strings.SplitSeq(text, "\n") {
		_, v, ok := parse(line)
		if ok && v == verdict {
			return
		}
	}
	t.Fatalf("no parseable gate.log line with verdict %q, got:\n%s", verdict, text)
}

// RequireNoMutantsMeasurement fails when gate.log carries any verdict from a
// run that reached the tool: "it was never measured" and "it measured clean"
// are different claims.
func RequireNoMutantsMeasurement(t *testing.T, cfgDir string, parse GateLineFields) {
	t.Helper()
	for line := range strings.SplitSeq(GateLogText(t, cfgDir), "\n") {
		if _, verdict, ok := parse(line); ok && strings.HasPrefix(verdict, "mutants-passed:") {
			t.Errorf("a measurement was recorded where none should have run: %s", line)
		}
	}
}

// ReadEscapes is every record the escape log at path holds, nil when there is
// no log.
func ReadEscapes[R any](t *testing.T, path string) []R {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []R
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r R
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("escapes.jsonl line is not JSON: %v (%s)", err, line)
		}
		out = append(out, r)
	}
	return out
}

// ForeignChainSample builds a fully-resolved, definitely-foreign process at
// pid leaf: a synthetic ancestor chain long enough to satisfy the ancestry
// walk's own bound (depth) with every hop valid and strictly earlier-created
// going up, never touching any pid a test uses as self. sample builds one
// process sample. It is shared with the integration tests that run against
// the REAL test process's own pid and so cannot risk a chain that merely looks
// foreign without being provably so under the stricter, creation-time-aware
// classifier (#548 cold review, RED 1).
func ForeignChainSample[S any](leaf int, name string, pctOneCore, cpuHours float64, depth int, sample func(pid, ppid int, name string, pctOneCore, cpuHours float64, creation uint64) S) []S {
	samples := []S{sample(leaf, leaf+1, name, pctOneCore, cpuHours, 1_000_000)}
	for i := 1; i <= depth+1; i++ {
		samples = append(samples, sample(leaf+i, leaf+i+1, "stranger.exe", 0, 0, uint64(1_000_000-i)))
	}
	return samples
}
