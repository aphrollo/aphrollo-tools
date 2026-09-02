package cli

import (
	"bytes"
	"io"
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
	base := t.TempDir()
	cwd := filepath.Join(base, "repo")
	lane := filepath.Join(base, "worktrees", "lane")
	cases := []struct {
		rest    []string
		want    string
		wantOK  bool
		comment string
	}{
		{[]string{"worktree", "remove", lane}, lane, true, "removal names the tree"},
		{[]string{"worktree", "remove", "--force", lane}, lane, true, "flags are skipped"},
		{[]string{"worktree", "remove", "lane"}, filepath.Join(cwd, "lane"), true, "a relative tree resolves against the verb's cwd"},
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

	lane := filepath.Join(t.TempDir(), "lane-x")
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

// TestWorktreeSweep_ResolvesAgainstTheCDir pins a wrong-tree deletion: git's
// own `-C <dir>` moves the working directory for the verb, so
// `git -C D:/repo worktree remove ../lane` removes D:/lane — but the sweep
// resolved "../lane" against the SHIM's cwd and would RemoveAll a directory
// in a completely different tree.
func TestWorktreeSweep_ResolvesAgainstTheCDir(t *testing.T) {
	base := t.TempDir()
	shimCwd := filepath.Join(base, "elsewhere", "session")
	repo := filepath.Join(base, "repos", "borld")

	got, ok := worktreeSweepTargetFor([]string{"-C", repo, "worktree", "remove", "../lane"}, shimCwd)
	if !ok {
		t.Fatal("worktree remove must qualify for a sweep")
	}
	want := filepath.Clean(filepath.Join(repo, "..", "lane"))
	if got != want {
		t.Fatalf("sweep target = %s, want %s (relative to -C, not to the shim's cwd)", got, want)
	}
}

// TestWorktreeSweep_RunsAfterTheLockIsReleased pins an avoidable stall: the
// sweep can RemoveAll tens of gigabytes, and doing it inside runGitWithLock's
// deferred release held the repo-scoped git lock for the whole walk — every
// other session's `git commit` queued behind a disk cleanup.
func TestWorktreeSweep_RunsAfterTheLockIsReleased(t *testing.T) {
	var order []string
	prev := gcAfterWorktreeChange
	gcAfterWorktreeChange = func(repoRoot, removed string) int64 {
		order = append(order, "sweep")
		return 0
	}
	t.Cleanup(func() { gcAfterWorktreeChange = prev })

	release := func() { order = append(order, "release") }
	runGitWithLock(release, "", gitStub(t), []string{"worktree", "prune"},
		strings.NewReader(""), io.Discard, io.Discard)

	if len(order) != 2 || order[0] != "release" || order[1] != "sweep" {
		t.Fatalf("order = %v, want the lock released before the sweep walks the disk", order)
	}
}
