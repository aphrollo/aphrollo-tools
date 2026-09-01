package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorktreeSweepTarget_ClassifiesTheTwoVerbsThatOrphanBuildDirs pins when
// a git invocation leaves a build directory behind: `worktree remove <path>`
// (git deletes the checkout and not the target/ inside it) and `worktree
// prune` (the registry drops entries whose trees are already gone).
// Everything else — including `worktree add` — leaves nothing to sweep.
func TestWorktreeSweepTarget_ClassifiesTheTwoVerbsThatOrphanBuildDirs(t *testing.T) {
	cwd := filepath.FromSlash("D:/Projects/borld")
	cases := []struct {
		rest    []string
		want    string
		wantOK  bool
		comment string
	}{
		{[]string{"worktree", "remove", filepath.FromSlash("D:/Projects/.worktrees/borld/lane")}, filepath.FromSlash("D:/Projects/.worktrees/borld/lane"), true, "removal names the tree"},
		{[]string{"worktree", "remove", "--force", filepath.FromSlash("D:/Projects/.worktrees/borld/lane")}, filepath.FromSlash("D:/Projects/.worktrees/borld/lane"), true, "flags are skipped"},
		{[]string{"worktree", "prune"}, "", true, "prune names no single tree"},
		{[]string{"worktree", "add", "-b", "x", "some/dir"}, "", false, "add creates, never orphans"},
		{[]string{"commit", "-m", "x"}, "", false, "unrelated verb"},
		{[]string{"worktree"}, "", false, "no sub-verb"},
	}
	for _, c := range cases {
		got, ok := worktreeSweepTarget(c.rest, cwd)
		if ok != c.wantOK || got != c.want {
			t.Errorf("worktreeSweepTarget(%v) = (%q, %v), want (%q, %v) — %s", c.rest, got, ok, c.want, c.wantOK, c.comment)
		}
	}
}

// TestRunGitShim_WorktreeRemovalSweepsItsBuildDir pins the wiring: a
// SUCCESSFUL worktree removal triggers the immediate, worktree-tied sweep
// for that repo, naming the tree that was removed. A build dir of tens of
// gigabytes surviving its own worktree is how a disk fills up unnoticed.
func TestRunGitShim_WorktreeRemovalSweepsItsBuildDir(t *testing.T) {
	withDirectGitShim(t)
	gitCommonDirEnv(t)
	cfg := testGitShimConfig(t)

	type sweep struct{ repo, removed string }
	var sweeps []sweep
	gcAfterWorktreeChange = func(repo, removed string) int64 {
		sweeps = append(sweeps, sweep{repo, removed})
		return 0
	}
	t.Cleanup(func() { gcAfterWorktreeChange = defaultGCAfterWorktreeChange })

	lane := filepath.FromSlash("D:/Projects/.worktrees/borld/lane-x")
	var stdout, stderr bytes.Buffer
	if code := runGitShim([]string{"worktree", "remove", lane}, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if len(sweeps) != 1 || sweeps[0].removed != lane {
		t.Fatalf("expected one sweep naming the removed tree, got %+v", sweeps)
	}
}

// TestRunGitShim_FailedWorktreeRemovalSweepsNothing pins the other half: if
// git refused the removal, the tree is still there and nothing may be
// deleted on its behalf.
func TestRunGitShim_FailedWorktreeRemovalSweepsNothing(t *testing.T) {
	withDirectGitShim(t)
	gitCommonDirEnv(t)
	t.Setenv("APHROLLO_TEST_STUB_FAIL_ON", "worktree")
	cfg := testGitShimConfig(t)

	var sweeps int
	gcAfterWorktreeChange = func(string, string) int64 { sweeps++; return 0 }
	t.Cleanup(func() { gcAfterWorktreeChange = defaultGCAfterWorktreeChange })

	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"worktree", "remove", filepath.FromSlash("D:/nope")}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code == 0 {
		t.Fatal("setup: the stub must have failed the removal")
	}
	if sweeps != 0 {
		t.Fatal("a failed removal must sweep nothing — the worktree is still there")
	}
}
