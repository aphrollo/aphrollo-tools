package tdd

import (
	"strings"
	"testing"
)

// The gofmt stage judged the files THIS COMMIT stages. The merge gate judges
// every file the LANE changed against its base. A file left unformatted by an
// earlier commit in the lane is therefore invisible to every later commit's
// gate and reappears at the merge, which is escape #216:
//
//	gate premergecommit: gofmt → REJECTED
//	  internal/workspace/pr_test.go
//
// after a pre-commit gate that had run green on the same tree. A guard's scan
// scope must equal its rule's scope, and the rule is that the lane merges
// formatted.
//
// Formatting is a parse, not a process spawn, so widening the set costs
// nothing measurable — and only gofmt widens. Lint and the suites stay scoped
// to the commit.
func TestPrecommit_GofmtJudgesAFileAnEarlierCommitInTheLaneLeftUnformatted(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)

	gitDo(t, root, "branch", "-f", "main")
	gitDo(t, root, "checkout", "-q", "-b", "lane/askew")

	// The lane's first commit lands an unformatted file. Nothing in a later
	// commit touches it again.
	write(t, root, "askew.go", "package m\n\nfunc Askew()  int  { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "-c", "core.hooksPath=", "commit", "-q", "-m", "lane commit one")

	// The commit actually being gated stages something else entirely, and is
	// itself formatted.
	write(t, root, "tidy.go", "package m\n\nfunc Tidy() int { return 2 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })

	if !res.Blocked {
		t.Fatal("the gate passed a lane carrying an unformatted file, so the merge gate is the first thing to see it — which is exactly the escape")
	}
	if !strings.Contains(res.Message, "askew.go") {
		t.Errorf("Message = %q, want it to name askew.go", res.Message)
	}
}

// ...and the widening must not reach past the lane onto files that were
// already unformatted on the base. Blocking a lane for a violation it did not
// introduce makes the gate unpassable and teaches people to bypass it; the
// merge gate does not judge those either.
func TestPrecommit_GofmtLeavesAFileTheLaneNeverTouchedToItsOwnLane(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)

	// Unformatted on the base branch, before the lane exists.
	write(t, root, "inherited.go", "package m\n\nfunc Inherited()  int  { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "-c", "core.hooksPath=", "commit", "-q", "-m", "base commit")
	gitDo(t, root, "branch", "-f", "main")
	gitDo(t, root, "checkout", "-q", "-b", "lane/tidy")

	write(t, root, "tidy.go", "package m\n\nfunc Tidy() int { return 2 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })

	if res.Blocked && strings.Contains(res.Message, "inherited.go") {
		t.Errorf("the gate blocked on a file the lane never touched: %q", res.Message)
	}
}
