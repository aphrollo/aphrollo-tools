package postedit

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Issue #730 is a DEFERRED-path defect: the real edit hook always runs its
// tests as detached build/run phases (postEditDeferred), and that path
// printed NO-TESTS-SELECTED for an empty narrowed selection without widening
// at all, on the claim that "the next edit's run widens" — which re-narrows
// to its own module instead. These tests drive that path.

// scriptedPhase is one detached phase's scripted answer: its exit and its
// log. A nil out is a phase that never finishes inside the test.
type scriptedPhase struct {
	out *PhaseOutcome
	log string
}

// phaseKey names a spawned phase by its argv, minus the -timeout= a Go run
// phase carries, so a script reads as the command a session would type.
func phaseKey(argv []string) string {
	var kept []string
	for _, a := range argv {
		if !strings.HasPrefix(a, "-timeout=") {
			kept = append(kept, a)
		}
	}
	return strings.Join(kept, " ")
}

// scriptedPhases replaces the detached-phase spawner with one that answers
// each phase from script, keyed by phaseKey, and records the order phases
// were spawned in. An unscripted phase fails the test rather than silently
// finishing green.
func scriptedPhases(t *testing.T, script map[string]scriptedPhase) *[]string {
	t.Helper()
	var spawned []string
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		key := phaseKey(j.Runner)
		spawned = append(spawned, key)
		p, ok := script[key]
		if !ok {
			t.Errorf("the gate spawned an unscripted phase: %q", key)
			return j, false
		}
		j.PID = 4000 + len(spawned)
		j.Started = time.Now()
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Session, j.Project)
		if p.out != nil {
			if err := os.WriteFile(j.Log, []byte(p.log), 0o600); err != nil {
				t.Error(err)
			}
			writePhaseResult(j.Result, *p.out)
		}
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })
	return &spawned
}

// inProcessPhases runs every detached phase for real, synchronously, through
// RunPhase itself — the wrapper `aphrollo gate runphase` executes — and
// records each RUN phase's command in order.
func inProcessPhases(t *testing.T) *[]string {
	t.Helper()
	var ran []string
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		saveDeferredJob(j)
		saved, ok := loadDeferredJob(j.Session, j.Project)
		if !ok {
			t.Fatalf("setup: the job record for %v did not persist", j.Runner)
		}
		_ = os.Remove(saved.Result)
		if saved.Phase == "run" {
			ran = append(ran, strings.Join(saved.Runner, " "))
		}
		RunPhase(deferredJobPath(saved.Session, saved.Project))
		return saved, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })
	return &ran
}

// requireRealNextest skips unless this box can run cargo-nextest for real.
func requireRealNextest(t *testing.T) {
	t.Helper()
	useRealCargoHome(t)
	for _, bin := range []string{"cargo", "cargo-nextest", "rustc"} {
		if _, err := exec.LookPath(bin); err != nil {
			// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever the toolchain is installed.
			t.Skipf("%s not on PATH; skipping the real-crate widening test", bin)
		}
	}
}

// TestPostEdit_DeferredZeroSelection_ClimbsModuleLibCrateOnARealCrate is the
// field shape end to end on a real crate: forge_lab's, whose whole suite is
// one integration target under tests/ with no lib tests at all. The hook's
// module filter selects nothing, the lib rung selects nothing, and the crate
// rung runs the integration test — the verdict the session used to get only
// by running the crate suite by hand.
func TestPostEdit_DeferredZeroSelection_ClimbsModuleLibCrateOnARealCrate(t *testing.T) {
	requireRealNextest(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"lab\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, ".config/nextest.toml", "[profile.default]\n")
	write(t, root, "src/lib.rs", "pub mod car;\n")
	write(t, root, "src/car.rs", "pub mod coast;\n")
	write(t, root, "src/car/coast.rs", "pub fn drag() -> i32 { 3 }\n")
	write(t, root, "tests/lab.rs", "#[test]\nfn coast_drag_is_three() { assert_eq!(lab::car::coast::drag(), 3); }\n")
	ran := inProcessPhases(t)

	got := PostEdit(postPayload("Edit", root+"/src/car/coast.rs"), fakeRun(true, "the foreground runner must not be used"))

	want := []string{
		"cargo nextest run -p lab --lib -E test(/^car::coast::/)",
		"cargo nextest run -p lab --lib",
		"cargo nextest run -p lab",
	}
	if strings.Join(*ran, "\n") != strings.Join(want, "\n") {
		t.Fatalf("run phases:\n%s\nwant the module filter, the lib rung, then the crate rung:\n%s\nline: %s",
			strings.Join(*ran, "\n"), strings.Join(want, "\n"), got)
	}
	if !strings.Contains(got, "gate: cargo nextest run -p lab in ") || !strings.Contains(got, "green (1 passed") {
		t.Fatalf("want the crate rung's green naming its command, got: %s", got)
	}
}

// TestPostEdit_DeferredWideningOutrunsTheBudget_ReportsTheRungBuildingNeverAGreen
// pins the budget half on the path the real hook takes: a rung still running
// when the edit's budget runs out keeps running, detached, and the line names
// THAT command — never a green for the narrower run, never a verdict the hook
// does not have.
func TestPostEdit_DeferredWideningOutrunsTheBudget_ReportsTheRungBuildingNeverAGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)
	narrow := "cargo nextest run -p engine_audio --lib -E test(/^defs::/)"
	lib := "cargo nextest run -p engine_audio --lib"
	spawned := scriptedPhases(t, map[string]scriptedPhase{
		narrow + " --no-run": {out: &PhaseOutcome{ExitCode: 0}},
		narrow:               {out: &PhaseOutcome{ExitCode: 4}, log: nextestNoTestsOutput},
		lib + " --no-run":    {},
	})

	got := PostEdit(postPayload("Edit", root+"/src/defs.rs"), fakeRun(true, "the foreground runner must not be used"))

	if n := len(*spawned); n != 3 {
		t.Fatalf("want the narrowed build+run then the lib rung's build, spawned %v", *spawned)
	}
	if strings.Contains(got, "green") {
		t.Fatalf("a rung still running must never read as green, got: %s", got)
	}
	if !strings.Contains(got, "BUILDING") || !strings.Contains(got, lib) {
		t.Fatalf("want a BUILDING line naming the rung still running (%s), got: %s", lib, got)
	}
	job, ok := loadDeferredJob("sess-post", root)
	if !ok || phaseKey(job.Runner) != lib+" --no-run" {
		t.Fatalf("the rung's job must be recorded for the next hook to harvest, got %+v (ok=%v)", job.Runner, ok)
	}
}

// TestPostEdit_HarvestedRungThatSelectedZero_ClimbsOnInsteadOfAnEmptyGreen
// pins the harvest half: a rung that finished after its hook returned, and
// selected nothing, used to be judged by editResultAdvisory as
// "green (0 tests — nothing to run)". The next hook climbs on from it instead.
func TestPostEdit_HarvestedRungThatSelectedZero_ClimbsOnInsteadOfAnEmptyGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)
	target := root + "/src/defs.rs"
	lib := "cargo nextest run -p engine_audio --lib"
	crate := "cargo nextest run -p engine_audio"
	spawned := scriptedPhases(t, map[string]scriptedPhase{
		crate + " --no-run": {out: &PhaseOutcome{ExitCode: 0}},
		crate:               {out: &PhaseOutcome{ExitCode: 0}, log: nextestSixPassedOutput},
	})
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-post", Phase: "run", Dir: root, PID: 999,
		Started: time.Now().Add(-time.Minute),
		HeadSHA: headSHAFor(root), FileHash: sourceIdentity(root, target), File: target,
		Runner: strings.Fields(lib),
	})
	job, _ := loadDeferredJob("sess-post", root)
	if err := os.WriteFile(job.Log, []byte(nextestNoTestsOutput), 0o600); err != nil {
		t.Fatal(err)
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 4, Seconds: 1})

	got := PostEdit(postPayload("Edit", target), fakeRun(true, "the foreground runner must not be used"))

	if strings.Contains(got, "0 tests — nothing to run") {
		t.Fatalf("a harvested empty selection must never print the empty-crate green, got: %s", got)
	}
	if strings.Join(*spawned, "|") != crate+" --no-run|"+crate {
		t.Fatalf("want the crate rung built and run after the harvested lib rung, spawned %v", *spawned)
	}
	if !strings.Contains(got, crate+" in ") || !strings.Contains(got, "green (6 passed") {
		t.Fatalf("want the crate rung's verdict naming its command, got: %s", got)
	}
}
