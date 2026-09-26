package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// undercoverShimRepo builds a one-commit repo on main, optionally declaring
// `undercover = true`, chdirs into it and returns it with a shim config wired
// to the real git: the wall must be judged on git's own answers (the current
// branch, whether a ref exists afterwards), which a stub cannot give.
func undercoverShimRepo(t *testing.T, on bool) (repo string, cfg gitShimConfig) {
	t.Helper()
	isolateGitConfigCLI(t)
	withDirectGitShim(t)
	realGit := realGitForTest(t)
	repo = t.TempDir()
	manifest := "[aphrollo]\n"
	if on {
		manifest += "undercover = true\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"checkout", "-q", "-B", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		runRealGit(t, realGit, repo, args...)
	}
	t.Chdir(repo)
	return repo, gitShimConfig{waitBudget: time.Second, pollInterval: 20 * time.Millisecond, realGit: realGit}
}

func runRealGit(t *testing.T, realGit, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func refExists(realGit, repo, branch string) bool {
	return exec.Command(realGit, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

func TestRunGitShim_RefusesATellBranchAtEveryCreatingVerb(t *testing.T) {
	repo, cfg := undercoverShimRepo(t, true)
	wt := filepath.Join(t.TempDir(), "wt")
	for _, c := range []struct {
		args   []string
		branch string
	}{
		{[]string{"checkout", "-b", "claude/x"}, "claude/x"},
		{[]string{"checkout", "-B", "lane/claude-fix"}, "lane/claude-fix"},
		{[]string{"switch", "-c", "Claude_x"}, "Claude_x"},
		{[]string{"switch", "--create=claude/y"}, "claude/y"},
		{[]string{"branch", "claude/z"}, "claude/z"},
		{[]string{"branch", "-f", "claude/z2", "main"}, "claude/z2"},
		{[]string{"branch", "-c", "main", "claude/copy"}, "claude/copy"},
		{[]string{"worktree", "add", "-b", "claude/wt", wt}, "claude/wt"},
	} {
		var out, errb bytes.Buffer
		code := runGitShim(c.args, strings.NewReader(""), &out, &errb, cfg)
		if code == 0 {
			t.Errorf("git %s: want a refusal, got exit 0", strings.Join(c.args, " "))
		}
		for _, want := range []string{`"` + c.branch + `"`, `"claude"`, "lane/<slug>"} {
			if !strings.Contains(errb.String(), want) {
				t.Errorf("git %s: refusal %q lacks %q", strings.Join(c.args, " "), errb.String(), want)
			}
		}
		if refExists(cfg.realGit, repo, c.branch) {
			t.Errorf("git %s: the branch was created anyway", strings.Join(c.args, " "))
		}
	}
}

func TestRunGitShim_PassesOrdinaryBranchNames(t *testing.T) {
	repo, cfg := undercoverShimRepo(t, true)
	wt := filepath.Join(t.TempDir(), "wt")
	for _, c := range []struct {
		args   []string
		branch string
	}{
		{[]string{"checkout", "-b", "lane/cairo"}, "lane/cairo"},
		{[]string{"switch", "-c", "lane/air-fix"}, "lane/air-fix"},
		{[]string{"branch", "lane/agents-doc"}, "lane/agents-doc"},
		{[]string{"worktree", "add", "-b", "lane/cursor-pagination", wt}, "lane/cursor-pagination"},
	} {
		var out, errb bytes.Buffer
		if code := runGitShim(c.args, strings.NewReader(""), &out, &errb, cfg); code != 0 {
			t.Errorf("git %s: exit %d: %s", strings.Join(c.args, " "), code, errb.String())
		}
		if !refExists(cfg.realGit, repo, c.branch) {
			t.Errorf("git %s: the branch was not created", strings.Join(c.args, " "))
		}
	}
}

// Deleting or listing a tell branch is the cleanup, never the leak.
func TestRunGitShim_LetsATellBranchBeDeletedOrListed(t *testing.T) {
	repo, cfg := undercoverShimRepo(t, true)
	runRealGit(t, cfg.realGit, repo, "branch", "claude/old")
	for _, args := range [][]string{
		{"branch", "--list", "claude/*"},
		{"branch", "-D", "claude/old"},
	} {
		var out, errb bytes.Buffer
		if code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg); code != 0 {
			t.Errorf("git %s: exit %d: %s", strings.Join(args, " "), code, errb.String())
		}
	}
	if refExists(cfg.realGit, repo, "claude/old") {
		t.Error("the tell branch was not deleted")
	}
}

func TestRunGitShim_RefusesPushingATellRef(t *testing.T) {
	repo, cfg := undercoverShimRepo(t, true)
	remote := t.TempDir()
	runRealGit(t, cfg.realGit, remote, "init", "-q", "--bare")
	runRealGit(t, cfg.realGit, repo, "branch", "claude/old")
	for _, c := range []struct {
		args []string
		ref  string
	}{
		{[]string{"push", remote, "HEAD:claude/x"}, "claude/x"},
		{[]string{"push", "-u", remote, "claude/old"}, "claude/old"},
		{[]string{"push", "--force", remote, "+main:lane/opus-4-eval"}, "lane/opus-4-eval"},
	} {
		var out, errb bytes.Buffer
		if code := runGitShim(c.args, strings.NewReader(""), &out, &errb, cfg); code == 0 {
			t.Errorf("git %s: want a refusal, got exit 0", strings.Join(c.args, " "))
		}
		if !strings.Contains(errb.String(), `"`+c.ref+`"`) {
			t.Errorf("git %s: refusal %q does not quote %q", strings.Join(c.args, " "), errb.String(), c.ref)
		}
	}
	runRealGit(t, cfg.realGit, repo, "checkout", "-q", "claude/old")
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"push", remote}, strings.NewReader(""), &out, &errb, cfg); code == 0 || !strings.Contains(errb.String(), `"claude/old"`) {
		t.Errorf("a bare push of a tell branch must be refused naming it, got exit %d: %s", code, errb.String())
	}
	if refs := strings.TrimSpace(gitShimOut(cfg.realGit, remote, "for-each-ref")); refs != "" {
		t.Errorf("the remote received refs anyway:\n%s", refs)
	}
}

func TestRunGitShim_PassesPushingAnOrdinaryRefOrDeletingATellOne(t *testing.T) {
	repo, cfg := undercoverShimRepo(t, true)
	remote := t.TempDir()
	runRealGit(t, cfg.realGit, remote, "init", "-q", "--bare")
	runRealGit(t, cfg.realGit, repo, "push", "-q", remote, "main:claude/leaked")
	for _, args := range [][]string{
		{"push", remote, "main:lane/cairo"},
		{"push", remote, "--delete", "claude/leaked"},
	} {
		var out, errb bytes.Buffer
		if code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg); code != 0 {
			t.Errorf("git %s: exit %d: %s", strings.Join(args, " "), code, errb.String())
		}
	}
	refs := gitShimOut(cfg.realGit, remote, "for-each-ref", "--format=%(refname)")
	if refs != "refs/heads/lane/cairo" {
		t.Errorf("remote refs = %q, want only refs/heads/lane/cairo", refs)
	}
}

func TestRunGitShim_UndercoverWallIsInertWhenTheRepoNeverAsked(t *testing.T) {
	repo, cfg := undercoverShimRepo(t, false)
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"checkout", "-b", "claude/x"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !refExists(cfg.realGit, repo, "claude/x") {
		t.Error("the branch was not created in a repo that never opted in")
	}
}
