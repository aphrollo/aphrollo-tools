package install

import (
	"strings"
	"testing"
)

const ciReuseQueueWorkflow = "on:\n  pull_request:\n  merge_group:\n    types: [checks_requested]\n  push:\n    branches: [main]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"

// A repo whose workflow listens on merge_group runs every pull request's
// pipeline twice unless something reuses the pull request's green run: the row
// warns, and points at the README snippet that fixes it.
func TestDoctor_WarnsWhenAWorkflowListensOnTheMergeQueueButNeverReusesAGreenRun(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo, ciReuseQueueWorkflow)

	c := check(t, Doctor(in), "CI queue reuse")
	if !c.OK || !c.Warn {
		t.Fatalf("a merge_group workflow with no reuse step must warn (OK %v Warn %v)", c.OK, c.Warn)
	}
	if !strings.Contains(c.Detail, "aphrollo ci reuse") || !strings.Contains(c.Detail, "README") {
		t.Errorf("detail %q does not name the verb and the README snippet", c.Detail)
	}
}

func TestDoctor_AcceptsAMergeQueueWorkflowThatAsksAphrolloCiReuse(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo, ciReuseQueueWorkflow+"      - run: ./bin/aphrollo ci reuse -repo o/r\n")

	c := check(t, Doctor(in), "CI queue reuse")
	if !c.OK || c.Warn {
		t.Fatalf("a workflow with the reuse step must pass quietly: %+v", c)
	}
}

// merge_group in a comment or a string is not a trigger.
func TestDoctor_SkipsTheQueueReuseRowWithoutAMergeGroupTrigger(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo, "on:\n  push:\n# merge_group: not yet\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: echo merge_group\n")
	for _, name := range checkNames(Doctor(in)) {
		if name == "CI queue reuse" {
			t.Fatal("the row must not appear for a repo with no merge_group trigger")
		}
	}
}
