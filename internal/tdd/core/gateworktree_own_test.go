package core

import (
	"strings"
	"testing"
)

// TestReadStagedTree_RefusesAnEmptyWorktree proves no worktree path never
// means "the directory this process stands in": read-tree -u --reset there
// would replace that checkout's files with another repo's index. The test
// stands in a scratch directory so that a regression cannot reach a real one.
func TestReadStagedTree_RefusesAnEmptyWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := readStagedTree(t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "no gate worktree") {
		t.Fatalf("readStagedTree(repo, \"\") = %v, want a refusal naming the missing worktree", err)
	}
}
