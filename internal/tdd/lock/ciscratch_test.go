package lock

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCIScratchRootOf_IsTheCIDirOfTheReposWorktreeLayout(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "widgets")

	got := CIScratchRootOf(primary)

	want := filepath.Join(filepath.Dir(primary), ".worktrees", "widgets", ".ci")
	if got != want {
		t.Errorf("CIScratchRootOf = %q, want %q", got, want)
	}
	if CIScratchRootOf("") != "" {
		t.Errorf("an empty primary must give no root")
	}
}

// Test code a job runs nests its own temp dirs under the run's scratch, and git
// refuses a GIT_DIR past the Windows path limit of 260 characters. The base
// plus a run's number, a job's directory and the deepest test path known (the
// package test root, its tmp dir, a test's own temp dir and a repository inside
// it) must fit under it for a project on the shortest realistic root.
func TestCIScratchRootOf_LeavesTheDeepestKnownTestPathRoomUnderTheWindowsLimit(t *testing.T) {
	const run = `\99999`
	const job = `\mutants-verdict`
	const deepest = `\aphrollo-tdd-pkgtest-1234567890\tmp\TestScanGC_TempScratchScopeSweepsWhereALocalCIRunMakesItsScratch1234567890\001\.git\objects\pack\tmp_pack_123456`
	root := strings.ReplaceAll(CIScratchRootOf(`D:\Projects\aphrollo-tools`), "/", `\`)

	total := len(root) + len(run) + len(job) + len(deepest)

	if total >= 260 {
		t.Errorf("%d characters (base %q) leave no room under the 260 limit", total, root)
	}
}
