package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// A lane worktree removed out from under git — its files gone, but nothing
// ever told git so via `worktree remove` — leaves --git-common-dir unable to
// answer for it: the exact state a job that outlives its lane's removal can
// see. Falling back to treating the lane as its own primary here reproduced
// precisely the nested path MutantsWorktreeDir's own doc comment warns
// against; four such fossils turned up on disk across two repos (issue
// #515). The resolver must refuse, not guess.
func TestMutantsRootDir_RefusesRatherThanNestUnderTheLaneWhenPrimaryUnresolvable(t *testing.T) {
	root := makeCargoRepo(t)
	parent := filepath.Dir(root)
	lane := filepath.Join(parent, ".worktrees", filepath.Base(root), "orphan-lane")
	gitDo(t, root, "worktree", "add", "-b", "lane/orphan", lane)

	if err := os.RemoveAll(lane); err != nil {
		t.Fatal(err)
	}

	if got := MutantsRootDir(lane); got != "" {
		t.Fatalf("MutantsRootDir(lane) = %q, want \"\" — the primary checkout could not be resolved", got)
	}
	if got := MutantsWorktreeDir(lane); got != "" {
		t.Fatalf("MutantsWorktreeDir(lane) = %q, want \"\"", got)
	}
	if got := MutantsTargetDir(lane); got != "" {
		t.Fatalf("MutantsTargetDir(lane) = %q, want \"\"", got)
	}
}
