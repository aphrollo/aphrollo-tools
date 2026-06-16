package workspace

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// preparedRepo builds an isolated repo with one prepared worktree on feat/x and
// returns (repo toplevel, worktree path, branch). It reuses the prepare plan so
// the worktree lands at exactly the path ResolveTarget computes.
func preparedRepo(t *testing.T) (repo, wt, branch string) {
	t.Helper()
	repo = initRepo(t)
	branch = "feat/x"
	plan, err := BuildPlan(Request{Repo: repo, Branch: branch, NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("prepare Apply: %v\n%s", err, errb.String())
	}
	return plan.Repo, plan.Worktree, branch
}

func TestResolveTarget_Cwd(t *testing.T) {
	repo := initRepo(t)
	t.Chdir(repo)
	tg, err := ResolveTarget("", "", "")
	if err != nil {
		t.Fatalf("ResolveTarget(cwd): %v", err)
	}
	if tg.Branch != "main" {
		t.Errorf("Branch = %q, want main", tg.Branch)
	}
	if tg.Worktree != tg.MainRepo {
		t.Errorf("on the main tree Worktree (%q) should equal MainRepo (%q)", tg.Worktree, tg.MainRepo)
	}
	if filepath.Base(tg.MainRepo) != filepath.Base(repo) {
		t.Errorf("RepoName resolved to %q, want base %q", tg.RepoName, filepath.Base(repo))
	}
}

func TestResolveTarget_CwdInsideWorktree(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	t.Chdir(wt)
	tg, err := ResolveTarget("", "", "")
	if err != nil {
		t.Fatalf("ResolveTarget(cwd in worktree): %v", err)
	}
	if tg.Branch != branch {
		t.Errorf("Branch = %q, want %q", tg.Branch, branch)
	}
	// Standing in a linked worktree: Worktree is the worktree, MainRepo is the clone.
	if filepath.Base(tg.Worktree) != "feat-x" {
		t.Errorf("Worktree = %q, want .../feat-x", tg.Worktree)
	}
	if tg.MainRepo != repo {
		t.Errorf("MainRepo = %q, want main clone %q", tg.MainRepo, repo)
	}
}

func TestResolveTarget_Positional(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	tg, err := ResolveTarget(repo, branch, "")
	if err != nil {
		t.Fatalf("ResolveTarget(positional): %v", err)
	}
	if tg.Worktree != wt {
		t.Errorf("Worktree = %q, want %q", tg.Worktree, wt)
	}
	if tg.MainRepo != repo || tg.Branch != branch {
		t.Errorf("got MainRepo=%q Branch=%q, want %q / %q", tg.MainRepo, tg.Branch, repo, branch)
	}
}

func TestResolveTarget_MixedArgs(t *testing.T) {
	for _, c := range [][2]string{{"/repo", ""}, {"", "branch"}} {
		if _, err := ResolveTarget(c[0], c[1], ""); err == nil {
			t.Errorf("ResolveTarget(%q,%q) should reject a half-specified target", c[0], c[1])
		}
	}
}

func TestResolveTarget_MissingWorktree_PointsToPrepare(t *testing.T) {
	repo := initRepo(t)
	_, err := ResolveTarget(repo, "never-prepared", "")
	if err == nil || !strings.Contains(err.Error(), "prepare") {
		t.Fatalf("expected a missing-worktree error pointing at prepare, got: %v", err)
	}
}

func TestMainWorktree_FromLinkedWorktree(t *testing.T) {
	repo, wt, _ := preparedRepo(t)
	got, err := mainWorktree(wt)
	if err != nil {
		t.Fatalf("mainWorktree: %v", err)
	}
	if got != repo {
		t.Errorf("mainWorktree(%q) = %q, want %q", wt, got, repo)
	}
}

func TestCurrentBranch_Detached(t *testing.T) {
	repo := initRepo(t)
	// Detach HEAD at the current commit.
	sha, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "checkout", "-q", strings.TrimSpace(string(sha))).CombinedOutput(); err != nil {
		t.Fatalf("detach: %v\n%s", err, out)
	}
	if b := currentBranch(repo); b != "HEAD" {
		t.Errorf("detached HEAD should report %q, got %q", "HEAD", b)
	}
}
