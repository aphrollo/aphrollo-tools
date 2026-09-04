package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// The guardrail now judges a shell command by the paths it would WRITE rather
// than by the session's cwd, so it no longer refuses housekeeping in another
// repo's worktree area. What it still did not say is WHICH path it objected
// to. The reported denial:
//
//	mv /d/Projects/.worktrees/aphrollo-tools/mutants …
//	gate: primary checkout is merge-only — git worktree add -b lane/<name> D:/Projects/.worktrees/borld/<name> main
//
// names a remedy in a third repository and no path at all, which sends the
// reader to fix the wrong thing. A denial has to name what it denied.
func TestPrimaryDecision_NamesThePathItRefused(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")
	addWorktreeForPrimary(t, root, "lane-a")
	target := filepath.Join(root, "main.go")

	got := bashPrimaryDecision(t.TempDir(), "printf x > "+filepath.ToSlash(target))

	if got.Action != Block {
		t.Fatalf("a write into the primary checkout must be blocked, got %+v", got)
	}
	if !strings.Contains(got.Reason, filepath.Base(target)) {
		t.Errorf("Reason = %q, want it to name %q — a denial that names no path sends the reader to the wrong repo", got.Reason, target)
	}
}

// ...and an edit judged by its own file path says the same thing, so the two
// enforcement points do not disagree about what a denial looks like.
func TestPrimaryBlock_WithNoPathStillNamesTheRemedy(t *testing.T) {
	root := makeGoRepo(t)

	got := primaryBlock(root)

	if !strings.Contains(got.Reason, "merge-only") {
		t.Errorf("Reason = %q, want the merge-only remedy", got.Reason)
	}
}

func addWorktreeForPrimary(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	gitDo(t, root, "worktree", "add", "-b", name, dir)
	return dir
}
