package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the staleness fix: `prepare` must fetch and base new work on
// the fresh default remote branch, NOT the operator's one-shot-seeded (and so
// drifting) local clone HEAD. Fixtures build a real upstream + clone so the gap
// between origin/<default> and the stale local ref is observable.

// isolateGit drops the operator box's global/system git config + the hook env so
// fixture git ops target the throwaway repos, never the real tree. Same guard
// initRepo applies, factored out for the origin-backed fixtures below.
func isolateGit(t *testing.T) {
	t.Helper()
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

func gitOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// upstreamRepo builds a non-bare "remote" on branch defaultBranch with one
// commit. Advance it later (via advance) to model an upstream that has moved
// past a clone whose remote-tracking ref is stale.
func upstreamRepo(t *testing.T, defaultBranch string) string {
	t.Helper()
	dir := t.TempDir()
	gitOK(t, dir, "init", "-q", "-b", defaultBranch)
	gitOK(t, dir, "config", "user.email", "up@t")
	gitOK(t, dir, "config", "user.name", "up")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOK(t, dir, "add", ".")
	gitOK(t, dir, "commit", "-q", "-m", "A")
	return dir
}

// cloneOf makes a local clone of upstream. Like the operator's seeded clones,
// its origin/<default> only moves on an explicit fetch.
func cloneOf(t *testing.T, upstream string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", upstream, dir).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	gitOK(t, dir, "config", "user.email", "lo@t")
	gitOK(t, dir, "config", "user.name", "lo")
	return dir
}

// advance adds one commit to repo on its current branch and returns the new sha.
func advance(t *testing.T, repo, file, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, file), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOK(t, repo, "add", ".")
	gitOK(t, repo, "commit", "-q", "-m", msg)
	return gitOK(t, repo, "rev-parse", "HEAD")
}

// TestPrepare_NewBranchBasesOnFreshOriginNotStaleLocal is the core regression:
// the clone is behind upstream; prepare --apply must fetch and start the new
// branch from the refreshed origin/main, not the stale local HEAD.
func TestPrepare_NewBranchBasesOnFreshOriginNotStaleLocal(t *testing.T) {
	isolateGit(t)
	up := upstreamRepo(t, "main")
	local := cloneOf(t, up)
	staleHEAD := gitOK(t, local, "rev-parse", "HEAD")
	freshHEAD := advance(t, up, "feature.go", "B")
	if staleHEAD == freshHEAD {
		t.Fatal("fixture: upstream did not advance")
	}
	// Pre-fetch the clone is genuinely stale: origin/main still points at A.
	if got := gitOK(t, local, "rev-parse", "origin/main"); got != staleHEAD {
		t.Fatalf("fixture: origin/main should be stale %s, got %s", staleHEAD, got)
	}

	plan, err := BuildPlan(Request{Repo: local, Branch: "feat/work", NoInstall: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout:%s\nstderr:%s", err, out.String(), errb.String())
	}

	wtHEAD := gitOK(t, plan.Worktree, "rev-parse", "HEAD")
	if wtHEAD == staleHEAD {
		t.Fatalf("worktree based on STALE local HEAD %s — fetch/start-point regressed", staleHEAD)
	}
	if wtHEAD != freshHEAD {
		t.Fatalf("worktree HEAD %s; want fresh origin/main %s", wtHEAD, freshHEAD)
	}
}

// TestPrepare_NonMainDefaultBranchResolves proves the default branch is read
// from origin/HEAD, not hardcoded to "main".
func TestPrepare_NonMainDefaultBranchResolves(t *testing.T) {
	isolateGit(t)
	up := upstreamRepo(t, "trunk")
	local := cloneOf(t, up)
	fresh := advance(t, up, "f.go", "B")

	plan, err := BuildPlan(Request{Repo: local, Branch: "feat/x", NoInstall: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.DefaultBranch != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", plan.DefaultBranch)
	}
	if plan.StartPoint != "origin/trunk" {
		t.Errorf("StartPoint = %q, want origin/trunk", plan.StartPoint)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if got := gitOK(t, plan.Worktree, "rev-parse", "HEAD"); got != fresh {
		t.Errorf("worktree HEAD %s, want origin/trunk %s", got, fresh)
	}
}

// TestPrepare_FetchFailureIsNonFatal: a repo with no origin remote must still
// prepare (off local HEAD), warning rather than failing on the dead fetch.
func TestPrepare_FetchFailureIsNonFatal(t *testing.T) {
	repo := initRepo(t) // no origin remote
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))

	plan, err := BuildPlan(Request{Repo: repo, Branch: "feat/solo", NoInstall: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.StartPoint != "" {
		t.Errorf("StartPoint = %q, want empty (no origin)", plan.StartPoint)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply must tolerate fetch failure, got: %v\n%s", err, errb.String())
	}
	if fi, err := os.Stat(plan.Worktree); err != nil || !fi.IsDir() {
		t.Fatalf("worktree not created despite non-fatal fetch fail: %v", err)
	}
	combined := strings.ToLower(out.String() + errb.String())
	if !strings.Contains(combined, "fetch") || !strings.Contains(combined, "warn") {
		t.Errorf("expected a non-fatal fetch warning, got:\n%s%s", out.String(), errb.String())
	}
}

// TestPrepare_DryRunListsFetchAndStartPoint: the plan preview must show the
// fetch step and the origin/main start-point before anything runs.
func TestPrepare_DryRunListsFetchAndStartPoint(t *testing.T) {
	isolateGit(t)
	up := upstreamRepo(t, "main")
	local := cloneOf(t, up)

	plan, err := BuildPlan(Request{Repo: local, Branch: "feat/x", NoInstall: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	dry := Render(plan, false)
	if !strings.Contains(dry, "fetch") {
		t.Errorf("dry-run missing fetch step:\n%s", dry)
	}
	if !strings.Contains(dry, "origin/main") {
		t.Errorf("dry-run missing origin/main start-point:\n%s", dry)
	}
}

// TestPrepare_ExistingBranchUntouchedWarnsWhenBehind: reusing an existing branch
// must NOT move it onto origin/main, but should warn when it's behind so the
// user can rebase.
func TestPrepare_ExistingBranchUntouchedWarnsWhenBehind(t *testing.T) {
	isolateGit(t)
	up := upstreamRepo(t, "main")
	local := cloneOf(t, up)
	branchBase := gitOK(t, local, "rev-parse", "HEAD")
	gitOK(t, local, "branch", "feat/old", branchBase)
	advance(t, up, "f.go", "B") // upstream moves ahead of feat/old

	plan, err := BuildPlan(Request{Repo: local, Branch: "feat/old", NoInstall: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !plan.BranchExists {
		t.Fatal("expected BranchExists for feat/old")
	}
	var addCmd []string
	for _, s := range plan.Steps {
		if s.Title == "create worktree" {
			addCmd = s.Cmd
		}
	}
	if strings.Contains(strings.Join(addCmd, " "), "origin/main") {
		t.Errorf("existing-branch worktree add must not start-point onto origin/main: %v", addCmd)
	}

	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if got := gitOK(t, plan.Worktree, "rev-parse", "HEAD"); got != branchBase {
		t.Errorf("existing branch HEAD moved to %s, want unchanged %s", got, branchBase)
	}
	if !strings.Contains(out.String(), "behind") {
		t.Errorf("expected a 'behind origin/main' warning:\n%s", out.String())
	}
}
