package postedit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sequencedPhases replaces the detached-phase spawner with one that answers
// the n-th spawn from outs[n] (nil: still running) and records every spawned
// argv in order. A spawn past the end of outs fails the test.
func sequencedPhases(t *testing.T, outs []*scriptedPhase) *[]string {
	t.Helper()
	var spawned []string
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		spawned = append(spawned, strings.Join(j.Runner, " "))
		if len(spawned) > len(outs) {
			t.Errorf("unexpected spawn %d: %q", len(spawned), strings.Join(j.Runner, " "))
			return j, false
		}
		p := outs[len(spawned)-1]
		if p != nil && p.onSpawn != nil {
			p.onSpawn()
		}
		j.PID = 5000 + len(spawned)
		j.Started = time.Now()
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Session, j.Project)
		if p != nil && p.out != nil {
			if err := os.WriteFile(j.Log, []byte(p.log), 0o600); err != nil {
				t.Error(err)
			}
			writePhaseResult(j.Result, *p.out)
		}
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	return &spawned
}

// TestWaitDeferredEditJob_RestartsTheRunWhenTheWaitedJobIsStale is issue
// #797's sequence. An edit's build phase is queued for a slot; a second edit
// finds it still running, marks it dirty and prints its BUILDING line; the
// build then ends without a slot. `gate status --wait` reported that job as
// "infra-failed — measured on an earlier tree state" and dropped it, and
// nothing had been started for the second edit's source: its BUILDING line
// had no job behind it, and the next --wait answered "no deferred edit job
// recorded". A wait whose job turns out stale starts the edit's run again on
// the tree as it stands and waits for THAT verdict.
func TestWaitDeferredEditJob_RestartsTheRunWhenTheWaitedJobIsStale(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir() // not a git repo: the source identity is the edited file's content
	file := filepath.Join(root, "src", "lib.rs")
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.5 }\n")
	runner := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_solver", "--lib", "-E", "test(/friction/)"}}
	const build = "cargo nextest run -p forge_solver --lib -E test(/friction/) --no-run"
	const run = "cargo nextest run -p forge_solver --lib -E test(/friction/)"
	green := &PhaseOutcome{ExitCode: 0, Seconds: 1}
	spawned := sequencedPhases(t, []*scriptedPhase{
		nil,          // the first edit's build, still queued when its hook returns
		{out: green}, // the build the wait restarts for the current source
		{out: green, log: "     Summary [   0.01s] 1 test run: 1 passed, 0 skipped\n"},
	})

	if out := runEditPhases(runner, root, file, "", sourceIdentity(root, file), "s-797", "", 0); !out.deferred {
		t.Fatalf("setup: want the first build left running, got %+v", out)
	}
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.4 }\n")
	if line, fresh := harvestDeferred(root, "", sourceIdentity(root, file), "s-797", 0, nil, ""); fresh || !strings.Contains(line, "BUILDING") {
		t.Fatalf("setup: the second edit's hook must find the build still running, got %q (fresh=%v)", line, fresh)
	}
	first, _ := loadDeferredJob("s-797", root)
	writePhaseResult(first.Result, PhaseOutcome{ExitCode: phaseSetupFailure, SetupFailed: true})

	advisory, ok := WaitDeferredEditJob(root)

	if !ok {
		t.Fatal("want a verdict, got ok=false")
	}
	if want := []string{build, build, run}; !reflect.DeepEqual(*spawned, want) {
		t.Fatalf("phases spawned:\n%q\nwant the stale build, then a build and run for the current source:\n%q\nadvisory: %s", *spawned, want, advisory)
	}
	if !strings.Contains(advisory, "measured on an earlier tree state") || !strings.Contains(advisory, "green (1 passed") {
		t.Fatalf("advisory = %q\nwant the stale job named as such AND the current source's own verdict", advisory)
	}
}

// TestWaitDeferredEditJob_RestartsOnceWhileTheTreeKeepsMoving: the restart is
// for the source as it stood when the wait found its job stale. A tree that
// moves again under the restarted run is the next hook's to chase; a wait
// that restarted every time would never return while a session kept editing.
func TestWaitDeferredEditJob_RestartsOnceWhileTheTreeKeepsMoving(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	file := filepath.Join(root, "src", "lib.rs")
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.5 }\n")
	runner := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_solver", "--lib"}}
	const build = "cargo nextest run -p forge_solver --lib --no-run"
	edit := func() { write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.3 }\n") }
	spawned := sequencedPhases(t, []*scriptedPhase{
		{out: &PhaseOutcome{ExitCode: 0, Seconds: 1}},
		{out: &PhaseOutcome{ExitCode: 0, Seconds: 1}, onSpawn: edit},
	})
	first := firstEditPhase(runner, root, file, "", sourceIdentity(root, file), "s-797", "")
	if _, ok := spawnPhaseFn(first); !ok {
		t.Fatal("setup: the first build did not record")
	}
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.4 }\n")

	advisory, ok := WaitDeferredEditJob(root)

	if !ok {
		t.Fatal("want the stale verdicts reported, got ok=false")
	}
	if want := []string{build, build}; !reflect.DeepEqual(*spawned, want) {
		t.Fatalf("phases spawned:\n%q\nwant the stale build and ONE restart:\n%q", *spawned, want)
	}
	if n := strings.Count(advisory, "measured on an earlier tree state"); n != 2 {
		t.Fatalf("advisory = %q\nwant both stale builds reported, got %d", advisory, n)
	}
}

// TestHarvestSessionJobs_RestartsTheStaleJobItDrops is the same fault on the
// sweep every Bash hook and prompt runs: it harvested the dirty job, reported
// it stale and dropped it, and the BUILDING line the second edit's hook had
// printed was left with no job behind it — `gate status --wait` then answered
// "no deferred edit job recorded for this checkout". The sweep starts the
// edit's run again for the source as it stands, as the wait does.
func TestHarvestSessionJobs_RestartsTheStaleJobItDrops(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	file := filepath.Join(root, "src", "lib.rs")
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.5 }\n")
	runner := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_solver", "--lib"}}
	const build = "cargo nextest run -p forge_solver --lib --no-run"
	spawned := sequencedPhases(t, []*scriptedPhase{nil, nil})
	if out := runEditPhases(runner, root, file, "", sourceIdentity(root, file), "s-797", "", 0); !out.deferred {
		t.Fatalf("setup: want the first build left running, got %+v", out)
	}
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.4 }\n")
	harvestDeferred(root, "", sourceIdentity(root, file), "s-797", 0, nil, "")
	first, _ := loadDeferredJob("s-797", root)
	writePhaseResult(first.Result, PhaseOutcome{ExitCode: phaseSetupFailure, SetupFailed: true})

	lines := harvestSessionJobs("s-797")

	if len(lines) != 1 || !strings.Contains(lines[0], "measured on an earlier tree state") {
		t.Fatalf("sweep lines = %q, want the dropped job reported stale", lines)
	}
	if want := []string{build, build}; !reflect.DeepEqual(*spawned, want) {
		t.Fatalf("phases spawned:\n%q\nwant the stale build, then a build for the current source:\n%q", *spawned, want)
	}
	next, ok := loadDeferredJob("s-797", root)
	if !ok || next.FileHash != sourceIdentity(root, file) {
		t.Fatalf("job after the sweep = %+v (found=%v), want one recorded for the current source", next, ok)
	}
}

// TestHarvestSessionJobs_LeavesAJobTheTreeMerelyMovedPast: a finished job no
// edit hook answered for — the source changed with no hook marking it dirty,
// as a commit or checkout does — is reported stale and not rerun: nobody was
// told a run for the newer source was under way.
func TestHarvestSessionJobs_LeavesAJobTheTreeMerelyMovedPast(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	file := filepath.Join(root, "src", "lib.rs")
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.5 }\n")
	runner := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_solver", "--lib"}}
	spawned := sequencedPhases(t, []*scriptedPhase{{out: &PhaseOutcome{ExitCode: 0, Seconds: 1}}})
	if _, ok := spawnPhaseFn(firstEditPhase(runner, root, file, "", sourceIdentity(root, file), "s-797", "")); !ok {
		t.Fatal("setup: the build did not record")
	}
	write(t, root, "src/lib.rs", "pub fn friction() -> f64 { 0.4 }\n")

	lines := harvestSessionJobs("s-797")

	if len(lines) != 1 || !strings.Contains(lines[0], "measured on an earlier tree state") {
		t.Fatalf("sweep lines = %q, want the job reported stale", lines)
	}
	if len(*spawned) != 1 {
		t.Fatalf("phases spawned: %q, want no rerun for a job no hook answered for", *spawned)
	}
}
