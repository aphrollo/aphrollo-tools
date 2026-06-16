package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// targetFor builds a Target for a repo's main tree without going through cwd
// resolution, so commit/push tests stay independent of the process working dir.
func targetFor(repo, branch string) *Target {
	return &Target{Worktree: repo, Branch: branch, MainRepo: repo, RepoName: filepath.Base(repo)}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitHEADSubject(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--pretty=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommit_StageAllAndApply(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")

	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, false)
	if err != nil {
		t.Fatalf("CommitPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if got := gitHEADSubject(t, repo); got != "add new.txt" {
		t.Errorf("HEAD subject = %q, want %q", got, "add new.txt")
	}
	if !strings.Contains(out.String(), "committed ") || !strings.Contains(out.String(), "add new.txt") {
		t.Errorf("Apply output missing precise feedback:\n%s", out.String())
	}
}

func TestCommit_CleanTreeIsNoOp(t *testing.T) {
	repo := initRepo(t)
	c, err := CommitPlan(targetFor(repo, "main"), "nothing", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Render(false), "nothing to commit") {
		t.Errorf("clean dry-run should say nothing to commit:\n%s", c.Render(false))
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply on clean tree should be a no-op, got: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to commit") {
		t.Errorf("Apply on clean tree should report no-op:\n%s", out.String())
	}
}

func TestCommit_EmptyMessageRejected(t *testing.T) {
	repo := initRepo(t)
	if _, err := CommitPlan(targetFor(repo, "main"), "   ", true, false); err == nil {
		t.Fatal("expected an error for an empty commit message")
	}
}

func TestCommit_StagedOnly_SkipsUnstaged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "staged.txt", "a\n")
	writeFile(t, repo, "loose.txt", "b\n")
	if out, err := exec.Command("git", "-C", repo, "add", "staged.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	c, err := CommitPlan(targetFor(repo, "main"), "only staged", false /*stageAll*/, false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	// loose.txt must remain untracked after a --staged-only commit.
	st, _ := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if !strings.Contains(string(st), "?? loose.txt") {
		t.Errorf("loose.txt should still be untracked after --staged-only:\n%s", st)
	}
}

func TestCommit_StagedOnly_NothingStaged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "loose.txt", "b\n") // present but never staged
	c, err := CommitPlan(targetFor(repo, "main"), "noop", false, false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(out.String(), "nothing staged") {
		t.Errorf("--staged-only with empty index should report nothing staged:\n%s", out.String())
	}
}
