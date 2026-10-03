package git

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func keyOf(t *testing.T, dir string) string {
	t.Helper()
	k, err := mustNew(t, dir).WorktreeKey()
	if err != nil {
		t.Fatalf("WorktreeKey(%s): %v", dir, err)
	}
	return k
}

func TestWorktreeKey_IsAStableSha256OfTheTreeAsItStands(t *testing.T) {
	dir := repoWithCommit(t)
	first, second := keyOf(t, dir), keyOf(t, dir)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Errorf("key %q is not 64 lower-case hex digits", first)
	}
	if first != second {
		t.Errorf("the same tree gave %q then %q", first, second)
	}
}

func TestWorktreeKey_NamesTheContentNotTheHistory(t *testing.T) {
	// Two repositories with different commits but the same tree and the same
	// untracked file, written in opposite order, are the same worktree.
	a, b := repoWithCommit(t), repoWithCommit(t)
	gitT(t, b, "commit", "-q", "--amend", "-m", "another message")
	write(t, a, "u1.txt", "one")
	write(t, a, "u2.txt", "two")
	write(t, b, "u2.txt", "two")
	write(t, b, "u1.txt", "one")
	if ka, kb := keyOf(t, a), keyOf(t, b); ka != kb {
		t.Errorf("same tree and untracked files gave different keys %q and %q", ka, kb)
	}
}

func TestWorktreeKey_MovesWithEveryKindOfChange(t *testing.T) {
	dir := repoWithCommit(t)
	clean := keyOf(t, dir)

	write(t, dir, "a.txt", "changed\n")
	modified := keyOf(t, dir)
	write(t, dir, "a.txt", "changed again\n")
	modifiedAgain := keyOf(t, dir)
	write(t, dir, "a.txt", "a\n")
	if back := keyOf(t, dir); back != clean {
		t.Errorf("reverting the edit gave key %q, want the clean tree's %q", back, clean)
	}

	write(t, dir, "new.txt", "x")
	untracked := keyOf(t, dir)
	write(t, dir, "new.txt", "y")
	untrackedAgain := keyOf(t, dir)
	if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	deleted := keyOf(t, dir)

	seen := map[string]string{"clean": clean}
	for name, k := range map[string]string{
		"modified": modified, "modified again": modifiedAgain,
		"untracked": untracked, "untracked other content": untrackedAgain, "deleted": deleted,
	} {
		if prev, dup := seen[k]; dup {
			t.Errorf("%s and %s share the key %q", name, prev, k)
		}
		seen[k] = name
	}
}

func TestWorktreeKey_StagingTheSameContentDoesNotMoveIt(t *testing.T) {
	dir := repoWithCommit(t)
	write(t, dir, "a.txt", "changed\n")
	unstaged := keyOf(t, dir)
	gitT(t, dir, "add", "a.txt")
	if staged := keyOf(t, dir); staged != unstaged {
		t.Errorf("staging moved the key from %q to %q: the key names the worktree's content, the index tree is another key", unstaged, staged)
	}
}

func TestWorktreeKey_AnIgnoredFileIsNotPartOfTheTree(t *testing.T) {
	dir := repoWithCommit(t)
	write(t, dir, ".gitignore", "*.log\n")
	gitT(t, dir, "add", ".gitignore")
	before := keyOf(t, dir)
	write(t, dir, "x.log", "noise")
	if after := keyOf(t, dir); after != before {
		t.Errorf("an ignored file moved the key from %q to %q", before, after)
	}
}

func TestWorktreeKey_ARenameIsTheOldPathGoneAndTheNewOneThere(t *testing.T) {
	dir := repoWithCommit(t)
	clean := keyOf(t, dir)
	gitT(t, dir, "mv", "a.txt", "b.txt")
	renamed := keyOf(t, dir)
	if renamed == clean {
		t.Fatal("renaming a tracked file left the key unchanged")
	}
	gitT(t, dir, "mv", "b.txt", "a.txt")
	if back := keyOf(t, dir); back != clean {
		t.Errorf("renaming back gave key %q, want the clean %q", back, clean)
	}
}

func TestWorktreeKey_ARepositoryWithNoCommitHasAKey(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	empty := keyOf(t, dir)
	write(t, dir, "f.txt", "x")
	if withFile := keyOf(t, dir); withFile == empty {
		t.Errorf("a file in an unborn repository did not move the key %q", empty)
	}
}

func TestWorktreeKey_ARepositoryGitCannotReadIsAnError(t *testing.T) {
	dir := repoWithCommit(t)
	c := mustNew(t, dir)
	if err := os.RemoveAll(filepath.Join(dir, ".git", "objects")); err != nil {
		t.Fatal(err)
	}
	if k, err := c.WorktreeKey(); err == nil {
		t.Errorf("a repository with no objects gave key %q, want an error", k)
	}
}
