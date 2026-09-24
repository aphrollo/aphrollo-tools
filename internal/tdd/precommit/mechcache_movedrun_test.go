package precommit

import (
	"testing"
)

// The merge gate's own cache write (#813's sibling). runSuiteStage keys its
// green on the state hashed before the run, which is right about what it was
// asked to test and silent about what it compiled: a tree that moved while
// the run was going — a mutation written from another shell mid-run — was
// compiled in its moved form, and the green then vouched for the unmoved
// state once the file was put back. Only a run whose tree held still from
// start to finish is a fact about that tree.
func TestMechanical_CachesNoGreenForATreeThatMovedDuringTheRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	staged := "pub fn step() -> f64 { 1.0 + f64::EPSILON }\n"

	moved := false
	first := Mechanical(root, func(r Runner, _ string) SuiteResult {
		if isSuiteVerb(r) && !moved {
			moved = true
			write(t, root, "crates/core/src/lib.rs", "pub fn step() -> f64 { 2.0 }\n")
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed\n"}
	})
	if first.Blocked {
		t.Fatalf("setup: unexpected block: %s", first.Message)
	}
	if !moved {
		t.Fatal("setup: the merge gate ran no suite")
	}
	write(t, root, "crates/core/src/lib.rs", staged)

	var ran []string
	out := captureStderr(t, func() {
		if res := Mechanical(root, suiteRuns(&ran)); res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
	})
	if len(ran) == 0 {
		t.Fatalf("the staged state was never compiled by a run that held still, yet its suite was not run; output:\n%s", out)
	}
}
