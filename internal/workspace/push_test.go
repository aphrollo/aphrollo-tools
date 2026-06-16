package workspace

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeGitHubURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:aphrollo/aphrollo-tools.git":       "https://github.com/aphrollo/aphrollo-tools",
		"https://github.com/aphrollo/aphrollo-tools.git":   "https://github.com/aphrollo/aphrollo-tools",
		"https://github.com/aphrollo/aphrollo-tools":       "https://github.com/aphrollo/aphrollo-tools",
		"ssh://git@github.com/aphrollo/aphrollo-tools.git": "https://github.com/aphrollo/aphrollo-tools",
		"git@gitlab.com:x/y.git":                           "",
		"/some/local/path":                                 "",
	}
	for in, want := range cases {
		if got := normalizeGitHubURL(in); got != want {
			t.Errorf("normalizeGitHubURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// repoWithRemote builds an isolated repo with a bare `origin` remote and main
// pushed, returning the repo toplevel.
func repoWithRemote(t *testing.T) string {
	t.Helper()
	repo := initRepo(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("remote", "add", "origin", bare)
	run("push", "-q", "-u", "origin", "main")
	return repo
}

func TestPush_NewBranchSetsUpstream(t *testing.T) {
	repo := repoWithRemote(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/y")
	writeFile(t, repo, "f.txt", "x\n")
	run("add", ".")
	run("commit", "-qm", "work")

	p, err := PushPlan(targetFor(repo, "feat/y"), false)
	if err != nil {
		t.Fatalf("PushPlan: %v", err)
	}
	if !strings.Contains(p.Render(false), "new branch") {
		t.Errorf("dry-run should detect a new branch:\n%s", p.Render(false))
	}
	var out, errb bytes.Buffer
	if err := p.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "pushed feat/y -> origin") {
		t.Errorf("Apply output missing push confirmation:\n%s", out.String())
	}
	// The branch now exists on origin, and upstream is set.
	p2, err := PushPlan(targetFor(repo, "feat/y"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p2.Render(false), "up to date") {
		t.Errorf("after push, second plan should be up to date:\n%s", p2.Render(false))
	}
}

func TestPush_AheadCount(t *testing.T) {
	repo := repoWithRemote(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	// Two commits ahead of origin/main.
	writeFile(t, repo, "a.txt", "1\n")
	run("add", ".")
	run("commit", "-qm", "c1")
	writeFile(t, repo, "b.txt", "2\n")
	run("add", ".")
	run("commit", "-qm", "c2")

	p, err := PushPlan(targetFor(repo, "main"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Render(false), "2 commit(s) ahead") {
		t.Errorf("expected 2 commits ahead:\n%s", p.Render(false))
	}
}

func TestPush_DetachedHEADRejected(t *testing.T) {
	if _, err := PushPlan(&Target{Worktree: "/x", Branch: "HEAD"}, false); err == nil {
		t.Fatal("expected detached-HEAD push to be rejected")
	}
}
