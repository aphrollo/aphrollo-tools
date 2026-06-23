package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs a git command in dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// repoWithOrigin builds a clone whose origin is a bare repo, both on `main`. It
// returns the clone path. The clone's origin/main tracks the bare repo, so a
// fetch/rebase against origin/main is exercised for real. git isolation matches
// initRepo (no global hooks/config leak into the fixture).
func repoWithOrigin(t *testing.T) string {
	t.Helper()
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	origin := t.TempDir()
	gitRun(t, origin, "init", "-q", "--bare", "-b", "main")

	seed := t.TempDir()
	gitRun(t, seed, "init", "-q", "-b", "main")
	gitRun(t, seed, "config", "user.email", "t@t")
	gitRun(t, seed, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-q", "-m", "seed")
	gitRun(t, seed, "remote", "add", "origin", origin)
	gitRun(t, seed, "push", "-q", "origin", "main")

	clone := t.TempDir()
	gitRun(t, clone, "clone", "-q", origin, clone)
	gitRun(t, clone, "config", "user.email", "t@t")
	gitRun(t, clone, "config", "user.name", "t")
	return clone
}

// advanceOrigin pushes a new commit onto origin/main from a fresh checkout, so
// the clone falls behind. file/content control whether it conflicts.
func advanceOrigin(t *testing.T, clone, file, content string) {
	t.Helper()
	tmp := t.TempDir()
	origin, err := exec.Command("git", "-C", clone, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, tmp, "clone", "-q", strings.TrimSpace(string(origin)), tmp)
	gitRun(t, tmp, "config", "user.email", "t@t")
	gitRun(t, tmp, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(tmp, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, tmp, "add", ".")
	gitRun(t, tmp, "commit", "-q", "-m", "advance origin")
	gitRun(t, tmp, "push", "-q", "origin", "main")
}

func TestUpdate_AlreadyCurrent(t *testing.T) {
	clone := repoWithOrigin(t)
	gitRun(t, clone, "checkout", "-q", "-b", "feat/up")

	var out, errb bytes.Buffer
	if err := Update(targetFor(clone, "feat/up"), false, &out, &errb); err != nil {
		t.Fatalf("Update: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "already current") {
		t.Errorf("up-to-date branch should report a no-op:\n%s", out.String())
	}
}

func TestUpdate_RebasesCleanAndPushes(t *testing.T) {
	clone := repoWithOrigin(t)
	gitRun(t, clone, "checkout", "-q", "-b", "feat/up")
	// branch commit on a NON-conflicting file
	if err := os.WriteFile(filepath.Join(clone, "feature.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "add", ".")
	gitRun(t, clone, "commit", "-q", "-m", "feature")
	gitRun(t, clone, "push", "-q", "-u", "origin", "feat/up")
	// origin/main advances on an unrelated file
	advanceOrigin(t, clone, "other.txt", "other\n")
	gitRun(t, clone, "fetch", "-q", "origin")

	var out, errb bytes.Buffer
	if err := Update(targetFor(clone, "feat/up"), false, &out, &errb); err != nil {
		t.Fatalf("Update: %v\n%s", err, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "rebased") {
		t.Errorf("clean rebase should be reported:\n%s", got)
	}
	if !strings.Contains(got, "pushed") {
		t.Errorf("an upstream branch should be force-pushed after rebase:\n%s", got)
	}
	// HEAD now contains origin's commit (other.txt present after the rebase).
	if _, err := os.Stat(filepath.Join(clone, "other.txt")); err != nil {
		t.Errorf("rebase should have applied origin/main's commit: %v", err)
	}
}

func TestUpdate_ConflictLeavesRebaseInProgress(t *testing.T) {
	clone := repoWithOrigin(t)
	gitRun(t, clone, "checkout", "-q", "-b", "feat/up")
	// branch edits base.txt
	if err := os.WriteFile(filepath.Join(clone, "base.txt"), []byte("branch change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "add", ".")
	gitRun(t, clone, "commit", "-q", "-m", "branch edit")
	// origin/main edits the SAME file differently => conflict on rebase
	advanceOrigin(t, clone, "base.txt", "origin change\n")
	gitRun(t, clone, "fetch", "-q", "origin")

	var out, errb bytes.Buffer
	err := Update(targetFor(clone, "feat/up"), false, &out, &errb)
	if err == nil {
		t.Fatalf("a rebase conflict must return non-nil error")
	}
	got := out.String() + errb.String()
	if !strings.Contains(got, "base.txt") {
		t.Errorf("conflict report should name the conflicted file:\n%s", got)
	}
	if !strings.Contains(got, "git rebase --continue") {
		t.Errorf("conflict report should tell the user how to resolve:\n%s", got)
	}
	// The rebase is left IN PROGRESS, not aborted.
	gitDir := filepath.Join(clone, ".git")
	_, e1 := os.Stat(filepath.Join(gitDir, "rebase-merge"))
	_, e2 := os.Stat(filepath.Join(gitDir, "rebase-apply"))
	if os.IsNotExist(e1) && os.IsNotExist(e2) {
		t.Errorf("rebase should be left in-progress for the user to resolve")
	}
}

func TestUpdate_DryReportsBehindWithoutMutating(t *testing.T) {
	clone := repoWithOrigin(t)
	gitRun(t, clone, "checkout", "-q", "-b", "feat/up")
	if err := os.WriteFile(filepath.Join(clone, "feature.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "add", ".")
	gitRun(t, clone, "commit", "-q", "-m", "feature")
	advanceOrigin(t, clone, "other.txt", "other\n")
	gitRun(t, clone, "fetch", "-q", "origin")

	headBefore, _ := exec.Command("git", "-C", clone, "rev-parse", "HEAD").Output()
	var out, errb bytes.Buffer
	if err := Update(targetFor(clone, "feat/up"), true, &out, &errb); err != nil {
		t.Fatalf("dry Update: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "behind") || !strings.Contains(out.String(), "would rebase") {
		t.Errorf("dry run should report behind-count + would-rebase:\n%s", out.String())
	}
	headAfter, _ := exec.Command("git", "-C", clone, "rev-parse", "HEAD").Output()
	if string(headBefore) != string(headAfter) {
		t.Errorf("dry run must not move HEAD")
	}
}
