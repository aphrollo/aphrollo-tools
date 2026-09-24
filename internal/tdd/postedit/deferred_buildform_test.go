package postedit

import (
	"reflect"
	"testing"
)

// Issue #798's sibling path: the edit hook's deferred build phase is the same
// build-then-run split the queue shim makes, and it appended --no-run to the
// runner as typed. nextest refuses --no-run beside --no-fail-fast, so a
// runner carrying it (the mutation prover's scoped run does) could never
// build. The build phase drops the run-only flag; the run phase — started in
// the same hook, or by a later harvest that has only the build's record to
// go on — gets it back.

const (
	buildFormRun   = "cargo nextest run -p sim --lib --no-fail-fast -E test(/friction/)"
	buildFormBuild = "cargo nextest run -p sim --lib -E test(/friction/) --no-run"
)

func buildFormRunner() Runner {
	return Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "sim", "--lib", "--no-fail-fast", "-E", "test(/friction/)"}}
}

func TestDeferredEditPhases_BuildDropsNoFailFastAndTheRunKeepsIt(t *testing.T) {
	root := t.TempDir()
	green := &PhaseOutcome{ExitCode: 0, Seconds: 1}
	spawned := scriptedPhases(t, map[string]scriptedPhase{
		buildFormBuild: {out: green},
		buildFormRun:   {out: green, log: "     Summary [   0.01s] 1 test run: 1 passed, 0 skipped\n"},
	})

	runEditPhases(buildFormRunner(), root, root+"/src/lib.rs", "head", "hash", "s-798", "", 5e9)

	if want := []string{buildFormBuild, buildFormRun}; !reflect.DeepEqual(*spawned, want) {
		t.Fatalf("phases spawned:\n%q\nwant the build without --no-fail-fast, then the run as typed:\n%q", *spawned, want)
	}
}

func TestDeferredHarvest_RunPhaseAfterABuildGetsTheRunOnlyFlagBack(t *testing.T) {
	root := t.TempDir()
	spawned := scriptedPhases(t, map[string]scriptedPhase{
		buildFormBuild: {}, // still building when the hook's budget runs out
		buildFormRun:   {out: &PhaseOutcome{ExitCode: 0, Seconds: 1}, log: "     Summary [   0.01s] 1 test run: 1 passed, 0 skipped\n"},
	})
	if out := runEditPhases(buildFormRunner(), root, root+"/src/lib.rs", "head", "hash", "s-798", "", 0); !out.deferred {
		t.Fatalf("setup: want the build left running, got %+v", out)
	}
	j, ok := loadDeferredJob("s-798", root)
	if !ok {
		t.Fatal("setup: no build job recorded")
	}
	writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	harvestDeferred(root, "head", "hash", "s-798", 5e9, nil, "")

	if want := []string{buildFormBuild, buildFormRun}; !reflect.DeepEqual(*spawned, want) {
		t.Fatalf("phases spawned:\n%q\nwant the harvest's run phase to carry --no-fail-fast again:\n%q", *spawned, want)
	}
}
