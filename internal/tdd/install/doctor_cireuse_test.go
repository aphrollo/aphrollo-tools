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

// The inline forms of the trigger are the same trigger.
func TestDoctor_SeesAMergeGroupTriggerWrittenInline(t *testing.T) {
	for name, body := range map[string]string{
		"a flow list":   "on: [push, merge_group]\njobs:\n  a:\n    runs-on: x\n",
		"a flow map":    "on: {push: {}, merge_group: {types: [checks_requested]}}\njobs:\n  a:\n    runs-on: x\n",
		"a scalar":      "on: merge_group\njobs:\n  a:\n    runs-on: x\n",
		"a quoted key":  "\"on\": [merge_group]\njobs:\n  a:\n    runs-on: x\n",
		"a trailing no": "on:\n  merge_group: # the queue\n    types: [checks_requested]\n",
	} {
		t.Run(name, func(t *testing.T) {
			in := healthyInstall(t)
			writeWorkflow(t, in.Repo, body)
			c := check(t, Doctor(in), "CI queue reuse")
			if !c.Warn {
				t.Errorf("no warning for %s: %+v", name, c)
			}
		})
	}
}

// A commented-out line is not a trigger and not a reuse step.
func TestDoctor_IgnoresCommentedTriggerAndCommentedReuse(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo, "on:\n  push:\n  # merge_group:\n#   types: [checks_requested]\njobs:\n  a:\n    runs-on: x\n")
	for _, name := range checkNames(Doctor(in)) {
		if name == "CI queue reuse" {
			t.Fatal("a commented merge_group made the row appear")
		}
	}

	in = healthyInstall(t)
	writeWorkflow(t, in.Repo, ciReuseQueueWorkflow+"      # - run: aphrollo ci reuse\n      - run: echo hi # aphrollo ci reuse\n")
	if c := check(t, Doctor(in), "CI queue reuse"); !c.Warn {
		t.Errorf("a reuse step that is only in a comment must still warn: %+v", c)
	}
}
