package workspace

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveMainRepo_AbsolutePath: a genuine absolute path to a repo resolves
// to its toplevel unchanged (the no-regression baseline).
func TestResolveMainRepo_AbsolutePath(t *testing.T) {
	repo := initRepo(t)
	got, err := resolveMainRepo(repo)
	if err != nil {
		t.Fatalf("resolveMainRepo(abs): %v", err)
	}
	if got != repo {
		t.Errorf("got %q, want %q", got, repo)
	}
}

// TestResolveMainRepo_RelativePath: a bare name that IS a valid cwd-relative
// path to a repo (run from the repo's parent) still resolves as before.
func TestResolveMainRepo_RelativePath(t *testing.T) {
	repo := initRepo(t)
	t.Chdir(filepath.Dir(repo))
	got, err := resolveMainRepo(filepath.Base(repo))
	if err != nil {
		t.Fatalf("resolveMainRepo(rel): %v", err)
	}
	if got != repo {
		t.Errorf("got %q, want %q", got, repo)
	}
}

// TestResolveMainRepo_FromInsideClone is the bug: a bare <repo> run from inside
// the clone must resolve to the clone, not cwd/<repo> (the double-join).
func TestResolveMainRepo_FromInsideClone(t *testing.T) {
	repo := initRepo(t)
	t.Chdir(repo)
	got, err := resolveMainRepo(filepath.Base(repo))
	if err != nil {
		t.Fatalf("resolveMainRepo(bare, from inside clone): %v", err)
	}
	if got != repo {
		t.Errorf("got %q, want clone %q (double-join not avoided)", got, repo)
	}
}

// TestResolveMainRepo_FromInsideWorktree: a bare <repo> run from inside a linked
// worktree resolves to the MAIN clone, not the worktree.
func TestResolveMainRepo_FromInsideWorktree(t *testing.T) {
	repo, wt, _ := preparedRepo(t)
	t.Chdir(wt)
	got, err := resolveMainRepo(filepath.Base(repo))
	if err != nil {
		t.Fatalf("resolveMainRepo(bare, from worktree): %v", err)
	}
	if got != repo {
		t.Errorf("got %q, want main clone %q", got, repo)
	}
}

// TestResolveMainRepo_SiblingDir: a bare <repo> run from a sibling clone under
// the same spaces owner resolves via the spaces glob.
func TestResolveMainRepo_SiblingDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APHROLLO_SPACES_ROOT", root)
	owner := filepath.Join(root, "aphrollo")
	web := initRepoAt(t, filepath.Join(owner, "aphrollo-web"))
	sibling := initRepoAt(t, filepath.Join(owner, "aphrollo-api"))
	t.Chdir(sibling)
	got, err := resolveMainRepo("aphrollo-web")
	if err != nil {
		t.Fatalf("resolveMainRepo(bare, from sibling): %v", err)
	}
	if got != web {
		t.Errorf("got %q, want sibling clone %q", got, web)
	}
}

// TestResolveMainRepo_Ambiguous: a bare name matching >1 spaces clone errors and
// lists the candidates rather than silently picking one.
func TestResolveMainRepo_Ambiguous(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APHROLLO_SPACES_ROOT", root)
	a := initRepoAt(t, filepath.Join(root, "owner-a", "shared"))
	b := initRepoAt(t, filepath.Join(root, "owner-b", "shared"))
	// Stand somewhere whose clone name is NOT "shared", so only the glob matches.
	t.Chdir(initRepo(t))
	_, err := resolveMainRepo("shared")
	if err == nil {
		t.Fatal("expected an ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should say ambiguous, got: %v", err)
	}
	for _, want := range []string{a, b} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error should list candidate %q, got: %v", want, err)
		}
	}
}

// TestResolveMainRepo_Unresolvable: an unresolvable <repo> fails with an
// actionable message naming what was tried and the fix — NOT the bare
// "<path> is not a git repository".
func TestResolveMainRepo_Unresolvable(t *testing.T) {
	root := t.TempDir() // empty spaces tree
	t.Setenv("APHROLLO_SPACES_ROOT", root)
	t.Chdir(initRepo(t))
	_, err := resolveMainRepo("does-not-exist")
	if err == nil {
		t.Fatal("expected an error for an unresolvable repo, got nil")
	}
	if strings.Contains(err.Error(), "is not a git repository") {
		t.Errorf("should not surface the bare gitToplevel error, got: %v", err)
	}
	for _, want := range []string{"does-not-exist", "absolute path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestResolveTarget_BareRepoName_FromInsideClone proves the git-verb addressing
// path (ResolveTarget -> resolveFromArgs) resolves a bare <repo> from inside the
// clone, landing on the prepared worktree — the end-to-end bug fix.
func TestResolveTarget_BareRepoName_FromInsideClone(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	t.Chdir(repo)
	tg, err := ResolveTarget(filepath.Base(repo), branch, "")
	if err != nil {
		t.Fatalf("ResolveTarget(bare, from inside clone): %v", err)
	}
	if tg.Worktree != wt {
		t.Errorf("Worktree = %q, want %q", tg.Worktree, wt)
	}
	if tg.MainRepo != repo {
		t.Errorf("MainRepo = %q, want %q", tg.MainRepo, repo)
	}
}

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

func TestResolveTarget_MissingWorktree_PointsToCreate(t *testing.T) {
	repo := initRepo(t)
	_, err := ResolveTarget(repo, "never-prepared", "")
	if err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("expected a missing-worktree error pointing at create, got: %v", err)
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
