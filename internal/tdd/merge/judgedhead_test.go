package merge

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// The merge verb judges the PR head GitHub will merge, not whatever the lane
// worktree has checked out: a lane with commits it never pushed would otherwise
// be judged on a tree the merge never makes (#1174). The gate takes the head
// it must judge, and builds the merge from that commit alone.

// laneWithAnUnpushedCommit is a lane whose first commit is small and clean and
// whose second, never pushed, takes a file past the module-size ceiling. head is
// the first commit: the one GitHub holds.
func laneWithAnUnpushedCommit(t *testing.T) (root, head string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root = t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	writeCrateSizeLaw(t, root, 15)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base and the law")
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	write(t, root, ".github/workflows/ci.yml", `on: pull_request
jobs:
  check:
    steps:
      - run: test ! -f late.txt
`)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane adds a small file")
	head = gitValue(t, root, "rev-parse", "HEAD")
	write(t, root, "crates/a/src/other.rs", bigFileLines("o", 30, 0))
	write(t, root, "late.txt", "unpushed\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane grows past the ceiling, never pushed")
	return root, head
}

func TestGatePRMerge_JudgesTheHeadItIsGivenNotTheCheckoutsHEAD(t *testing.T) {
	root, head := laneWithAnUnpushedCommit(t)
	run := recordRuns(new([]gateRun), SuiteResult{Passed: true})

	if err := GatePRMerge(root, "", run, io.Discard); err == nil || !strings.Contains(err.Error(), "module-size") {
		t.Fatalf("the checkout's HEAD breaks module-size, so judging it must refuse naming the law, got: %v", err)
	}
	if err := GatePRMerge(root, head, run, io.Discard); err != nil {
		t.Fatalf("the PR head is clean and is what merges, so it must pass: %v", err)
	}
}

func TestGatePRMergeReusingCI_JudgesTheHeadItIsGivenNotTheCheckoutsHEAD(t *testing.T) {
	root, head := laneWithAnUnpushedCommit(t)
	run := recordRuns(new([]gateRun), SuiteResult{Passed: true})
	v := CIVerdict{PR: 7, HeadSHA: head}

	if err := GatePRMergeReusingCI(root, head, run, io.Discard, v); err != nil {
		t.Fatalf("the PR head is clean and is what merges, so it must pass: %v", err)
	}
	if err := GatePRMergeReusingCI(root, "", run, io.Discard, v); err == nil || !strings.Contains(err.Error(), "module-size") {
		t.Fatalf("the checkout's HEAD breaks module-size, so judging it must refuse naming the law, got: %v", err)
	}
}

func TestGatePRMerge_RefusesAHeadThisCheckoutDoesNotHold(t *testing.T) {
	root, _ := laneWithAnUnpushedCommit(t)
	run := recordRuns(new([]gateRun), SuiteResult{Passed: true})
	unknown := strings.Repeat("9", 40)

	err := GatePRMerge(root, unknown, run, io.Discard)
	if err == nil || !strings.Contains(err.Error(), unknown[:7]) {
		t.Fatalf("a head that is not in the checkout was never judged, so it must refuse naming it, got: %v", err)
	}
}

func TestLocalCI_JudgesTheHeadItIsGivenNotTheCheckoutsHEAD(t *testing.T) {
	root, head := laneWithAnUnpushedCommit(t)

	var log bytes.Buffer
	if _, err := LocalCIWith(root, &log, CIRunOptions{Head: head}); err != nil {
		t.Fatalf("the PR head has no late.txt, so its workflow is green: %v\n%s", err, log.String())
	}
	if _, err := LocalCIWith(root, io.Discard, CIRunOptions{}); err == nil {
		t.Fatal("the checkout's HEAD holds late.txt, so judging it must be red")
	}
}
