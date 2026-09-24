package tdd

import (
	"strings"
	"testing"
)

// A large pure-move refactor leaves the nightly checkpoint far behind main:
// the next scheduled run would measure every moved line and overrun its
// timeout night after night. The dispatch takes a base so an operator can
// move the checkpoint past such a refactor, and the checkpoint step must
// honour it before falling back to the last successful run.
func TestNightlyMutantsWorkflow_DispatchTakesABaseTheCheckpointHonours(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-mutants.yml")

	dispatch := wf[strings.Index(wf, "workflow_dispatch:"):]
	dispatch = dispatch[:strings.Index(dispatch, "permissions:")]
	if !strings.Contains(dispatch, "inputs:") || !strings.Contains(dispatch, "base:") {
		t.Fatalf("workflow_dispatch declares no base input:\n%s", dispatch)
	}

	i := strings.Index(wf, "id: checkpoint")
	if i < 0 {
		t.Fatal("no checkpoint step in nightly-mutants.yml")
	}
	step := wf[i:]
	if j := strings.Index(step, "- name:"); j > 0 {
		step = step[:j]
	}
	in := strings.Index(step, "inputs.base")
	fallback := strings.Index(step, "gh run list")
	if in < 0 || fallback < 0 || in > fallback {
		t.Fatalf("the checkpoint step must read inputs.base before falling back to the last successful run:\n%s", step)
	}
}
