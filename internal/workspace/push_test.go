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

	// push folds the draft-PR open, so stub the gh + CI seams.
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			return &PRInfo{Number: 1, URL: "https://github.com/o/r/pull/1", State: "OPEN", IsDraft: req.Draft}, nil
		},
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "none"}, nil })

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

// indexOf returns the position of v in args, or -1 if absent.
func indexOf(args []string, v string) int {
	for i, a := range args {
		if a == v {
			return i
		}
	}
	return -1
}

func TestPushArgs_ForceWithLeaseBeforeTerminator(t *testing.T) {
	args := pushArgs("/wt", "feat/x", true)

	flag := indexOf(args, "--force-with-lease")
	term := indexOf(args, "--")
	if flag < 0 {
		t.Fatalf("--force-with-lease missing from args: %v", args)
	}
	if term < 0 {
		t.Fatalf("-- terminator missing from args: %v", args)
	}
	// After "--" git reads every token as a refspec, so the flag MUST precede
	// it — otherwise git sees the literal refspec "--force-with-lease".
	if flag > term {
		t.Errorf("--force-with-lease (idx %d) must come before -- (idx %d): %v", flag, term, args)
	}
	// And before "origin" too, where push flags belong.
	if origin := indexOf(args, "origin"); flag > origin {
		t.Errorf("--force-with-lease (idx %d) must come before origin (idx %d): %v", flag, origin, args)
	}
}

func TestPushArgs_NoForceWithLeaseWhenDisabled(t *testing.T) {
	args := pushArgs("/wt", "feat/x", false)
	if i := indexOf(args, "--force-with-lease"); i >= 0 {
		t.Errorf("--force-with-lease should be absent when disabled: %v", args)
	}
	// Branch still guarded behind the terminator.
	if term, branch := indexOf(args, "--"), indexOf(args, "feat/x"); term < 0 || branch != term+1 {
		t.Errorf("branch must immediately follow -- : %v", args)
	}
}

func TestPush_DetachedHEADRejected(t *testing.T) {
	if _, err := PushPlan(&Target{Worktree: "/x", Branch: "HEAD"}, false); err == nil {
		t.Fatal("expected detached-HEAD push to be rejected")
	}
}

// stubCI swaps the gh pr checks seam for the duration of a test.
func stubCI(t *testing.T, ci func(wt, branch string) (CIStatus, error)) {
	t.Helper()
	o := ghCIStatus
	ghCIStatus = ci
	t.Cleanup(func() { ghCIStatus = o })
}

// push now folds the draft-PR open: after pushing, it ensures a DRAFT PR exists
// (opening it when absent) and reports the stateful receipt — pushed line, the
// pr #N draft [opened] line, and the ci line — so the coder needs no follow-up.
func TestPush_OpensDraftPRAndReportsState(t *testing.T) {
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

	var created *PRCreate
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil }, // no existing PR
		func(wt string, req PRCreate) (*PRInfo, error) {
			created = &req
			return &PRInfo{Number: 9, URL: "https://github.com/o/r/pull/9", State: "OPEN", IsDraft: req.Draft}, nil
		},
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	p, err := PushPlan(targetFor(repo, "feat/y"), false)
	if err != nil {
		t.Fatalf("PushPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	s := out.String()
	if created == nil || !created.Draft {
		t.Fatalf("push must open a DRAFT PR, got: %+v", created)
	}
	if !strings.Contains(s, "pushed feat/y -> origin") {
		t.Errorf("receipt missing pushed line:\n%s", s)
	}
	if !strings.Contains(s, "pr #9 draft") || !strings.Contains(s, "opened") {
		t.Errorf("receipt missing pr draft/opened line:\n%s", s)
	}
	if !strings.Contains(s, "ci pending") {
		t.Errorf("receipt missing ci state line:\n%s", s)
	}
}

// A re-driven push must REUSE the existing draft PR, never open a second one.
func TestPush_ReusesExistingPR(t *testing.T) {
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

	createCalled := false
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 9, URL: "https://github.com/o/r/pull/9", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { createCalled = true; return nil, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	p, _ := PushPlan(targetFor(repo, "feat/y"), false)
	var out, errb bytes.Buffer
	if err := p.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if createCalled {
		t.Error("a re-driven push must reuse the existing PR, not open a second one")
	}
	if !strings.Contains(out.String(), "reused") {
		t.Errorf("receipt should report the PR was reused:\n%s", out.String())
	}
}
