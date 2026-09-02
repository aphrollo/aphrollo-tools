package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// The commit gate judges what is being COMMITTED — the index — not whatever
// happens to be on disk. A dirty worktree the author has not staged is not
// part of this commit, and rejecting it is a false positive that no amount of
// staging can clear.
func TestRatchetStage_IgnoresAnUnstagedOffendingEdit(t *testing.T) {
	root := lawTree(t, "deny")
	addFixtures(t, root)
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an unstaged edit is not part of the commit: %s", res.Message)
	}
}

// The other direction of the same bug, and the one that lets an offence
// through: the regression is STAGED and a later unstaged edit tidies the disk
// copy, so a gate reading the worktree sees a tree that is not being
// committed.
func TestRatchetStage_JudgesAStagedOffenceHiddenByAnUnstagedFix(t *testing.T) {
	root := lawTree(t, "deny")
	addFixtures(t, root)
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "lib.rs:2") {
		t.Fatalf("the staged content is what lands, and it regresses the law: %+v", res)
	}
}
