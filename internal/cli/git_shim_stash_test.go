package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These run against a REAL repo with real linked worktrees, exactly like
// git_shim_discard_test.go's fixtures: refs/stash's SHARING across
// worktrees is real git plumbing, not something a stub can produce
// truthfully.

// stashFixture builds a one-commit repo on main plus two linked worktrees on
// lane/a and lane/b, and returns a gitShimConfig for driving runGitShim.
func stashFixture(t *testing.T) (repo, wtA, wtB string, cfg gitShimConfig) {
	t.Helper()
	withDirectGitShim(t)
	isolateGitConfigCLI(t)
	realGit := realGitForTest(t)
	repo = t.TempDir()
	runFixtureGit(t, realGit, repo, "init", "-q", "-b", "main")
	runFixtureGit(t, realGit, repo, "config", "user.email", "t@t")
	runFixtureGit(t, realGit, repo, "config", "user.name", "t")
	writeFixtureFile(t, repo, "seed.txt", []string{"seed"})
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "seed")

	wtA = filepath.Join(t.TempDir(), "a")
	wtB = filepath.Join(t.TempDir(), "b")
	runFixtureGit(t, realGit, repo, "worktree", "add", "-b", "lane/a", wtA, "main")
	runFixtureGit(t, realGit, repo, "worktree", "add", "-b", "lane/b", wtB, "main")

	return repo, wtA, wtB, gitShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realGit:      realGit,
	}
}

// dirtyFixtureFile makes an uncommitted change in dir so `git stash` has
// something to stash.
func dirtyFixtureFile(t *testing.T, dir, content string) {
	t.Helper()
	writeFixtureFile(t, dir, "seed.txt", []string{content})
}

func stashList(t *testing.T, realGit, dir string) string {
	t.Helper()
	return runFixtureGit(t, realGit, dir, "stash", "list")
}

// A bare `git stash pop` in lane/b must not take lane/a's entry: refused,
// and the entry is still on the stack afterward.
func TestGitShim_RefusesPoppingAnotherLanesStash(t *testing.T) {
	gateConfigDir(t)
	_, wtA, wtB, cfg := stashFixture(t)

	t.Chdir(wtA)
	dirtyFixtureFile(t, wtA, "a-wip")
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"stash", "push", "-m", "lane/a: wip"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("labelled push from lane/a failed: exit %d\n%s", code, errb.String())
	}

	t.Chdir(wtB)
	errb.Reset()
	code := runGitShim([]string{"stash", "pop"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("stash pop from lane/b must be refused, took lane/a's entry instead")
	}
	msg := errb.String()
	for _, want := range []string{"lane/a", "#384"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q missing %q", msg, want)
		}
	}
	if !strings.Contains(stashList(t, cfg.realGit, wtB), "lane/a: wip") {
		t.Fatal("lane/a's stash entry must still be present after the refused pop")
	}
}

// A labelled push from each lane, then a pop from the lane that owns the TOP
// entry, must leave the other lane's entry untouched.
func TestGitShim_LabelledPushesThenAnOwnedPopLeavesTheOtherLanesEntryUntouched(t *testing.T) {
	gateConfigDir(t)
	_, wtA, wtB, cfg := stashFixture(t)

	t.Chdir(wtA)
	dirtyFixtureFile(t, wtA, "a-wip")
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"stash", "push", "-m", "lane/a: wip"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("labelled push from lane/a failed: exit %d\n%s", code, errb.String())
	}

	t.Chdir(wtB)
	dirtyFixtureFile(t, wtB, "b-wip")
	errb.Reset()
	if code := runGitShim([]string{"stash", "push", "-m", "lane/b: wip"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("labelled push from lane/b failed: exit %d\n%s", code, errb.String())
	}

	// lane/b owns the top entry (pushed last) — its own pop must succeed.
	errb.Reset()
	if code := runGitShim([]string{"stash", "pop"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("lane/b popping its own top entry was refused: %s", errb.String())
	}
	if strings.Contains(stashList(t, cfg.realGit, wtB), "lane/b: wip") {
		t.Fatal("lane/b's own entry should have been popped")
	}
	if !strings.Contains(stashList(t, cfg.realGit, wtB), "lane/a: wip") {
		t.Fatal("lane/a's entry must still be on the stack, untouched")
	}
}

// A bare `git stash push` (no -m) in a multi-worktree repo is refused, so
// every future entry carries the label the pop-side check reads.
func TestGitShim_RefusesAnUnlabelledStashPushInAMultiWorktreeRepo(t *testing.T) {
	gateConfigDir(t)
	_, wtA, _, cfg := stashFixture(t)

	t.Chdir(wtA)
	dirtyFixtureFile(t, wtA, "a-wip")
	var out, errb bytes.Buffer
	code := runGitShim([]string{"stash"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatal("an unlabelled `git stash` must be refused in a multi-worktree repo")
	}
	if !strings.Contains(errb.String(), "#384") {
		t.Errorf("refusal %q does not cite the issue", errb.String())
	}
	if !strings.Contains(runFixtureGit(t, cfg.realGit, wtA, "status", "--porcelain"), "seed.txt") {
		t.Fatal("the refused push must not have run: the dirty file should still be uncommitted, not stashed")
	}
}

// With only ONE worktree, refs/stash is not shared with anyone: the guard
// must not engage at all, for either push or pop.
func TestGitShim_StashGuardDoesNotEngageWithASingleWorktree(t *testing.T) {
	gateConfigDir(t)
	withDirectGitShim(t)
	isolateGitConfigCLI(t)
	realGit := realGitForTest(t)
	repo := t.TempDir()
	runFixtureGit(t, realGit, repo, "init", "-q", "-b", "main")
	runFixtureGit(t, realGit, repo, "config", "user.email", "t@t")
	runFixtureGit(t, realGit, repo, "config", "user.name", "t")
	writeFixtureFile(t, repo, "seed.txt", []string{"seed"})
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "seed")

	cfg := gitShimConfig{waitBudget: time.Second, pollInterval: 20 * time.Millisecond, realGit: realGit}
	t.Chdir(repo)
	dirtyFixtureFile(t, repo, "solo-wip")
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"stash"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("an unlabelled stash push in a single-worktree repo must pass: exit %d\n%s", code, errb.String())
	}
	errb.Reset()
	if code := runGitShim([]string{"stash", "pop"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("a bare stash pop in a single-worktree repo must pass: exit %d\n%s", code, errb.String())
	}
}
