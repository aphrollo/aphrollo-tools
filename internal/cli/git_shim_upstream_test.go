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

// The merge-only wall refuses any pull or merge that could fast-forward,
// because a fast-forward fires no hook and would land lane commits on main
// unjudged. Fast-forwarding main to its OWN UPSTREAM is the one case where
// that reasoning does not hold: those commits were judged on the way INTO
// origin/main, and the pull adds nothing the local gate has any say over.
//
// Refusing it broke the ordinary loop of a PR-only repository. Reported:
// `git pull --ff-only origin main` in the primary checkout was refused with
// `gate: primary checkout is merge-only — git worktree add -b lane/<name> …`,
// the primary stayed at dac0afa while origin/main was 574c211, and
// `gate self-install` then built and installed the STALE tree. The only way
// through was APHROLLO_PRIMARY_EDITS=1, which switches the whole wall off.
func TestRunGitShim_AllowsFastForwardingThePrimaryToItsOwnUpstream(t *testing.T) {
	cfg, primary := primaryWithUpstream(t)

	for _, argv := range [][]string{
		{"pull", "--ff-only", "origin", "main"},
		{"pull", "--ff-only"},
		{"merge", "--ff-only", "origin/main"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			var out, errb bytes.Buffer
			code := runGitShim(argv, strings.NewReader(""), &out, &errb, cfg)
			if strings.Contains(errb.String(), "merge-only") {
				t.Fatalf("git %v was refused: %q — the commits were judged on their way into origin/main", argv, errb.String())
			}
			if code != 0 {
				t.Fatalf("git %v exited %d\n%s", argv, code, errb.String())
			}
		})
	}
	_ = primary
}

// ...and nothing else opens up: a fast-forward from a LANE is still the thing
// the wall exists for, because those commits have never been judged anywhere.
func TestRunGitShim_StillRefusesAFastForwardFromALaneBranch(t *testing.T) {
	cfg, _ := primaryWithUpstream(t)

	for _, argv := range [][]string{
		{"merge", "lane/x"},
		{"pull", "origin", "lane/x"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			var out, errb bytes.Buffer
			runGitShim(argv, strings.NewReader(""), &out, &errb, cfg)
			if !strings.Contains(errb.String(), "merge-only") {
				t.Errorf("git %v was allowed: a lane fast-forward lands commits on main with no hook to judge them", argv)
			}
		})
	}
}

// primaryWithUpstream is a primary checkout on main, tracking origin/main in a
// real remote that is one commit AHEAD — the state a PR-only repository is in
// every time a pull request is merged on the server.
func primaryWithUpstream(t *testing.T) (gitShimConfig, string) {
	t.Helper()
	isolateGitConfigCLI(t)
	withDirectGitShim(t)
	realGit := realGitForTest(t)
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	origin := filepath.Join(t.TempDir(), "origin")
	run(t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)

	seed := t.TempDir()
	run(seed, "init", "-q")
	run(seed, "config", "user.email", "t@example.com")
	run(seed, "config", "user.name", "t")
	run(seed, "checkout", "-q", "-B", "main")
	mustWriteFile(t, filepath.Join(seed, "main.go"), "package main\n")
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "init")
	run(seed, "remote", "add", "origin", origin)
	run(seed, "push", "-q", "origin", "main")

	primary := filepath.Join(t.TempDir(), "primary")
	run(t.TempDir(), "clone", "-q", origin, primary)
	run(primary, "config", "user.email", "t@example.com")
	run(primary, "config", "user.name", "t")
	run(primary, "branch", "lane/x")
	// The wall only stands once the repo has a linked worktree: that is what
	// makes this checkout the merge-only one.
	run(primary, "worktree", "add", "-q", filepath.Join(t.TempDir(), "lane"), "lane/x")

	// origin moves ahead, the way a merged pull request leaves it.
	mustWriteFile(t, filepath.Join(seed, "merged.go"), "package main\n\nfunc Merged() {}\n")
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "a merged pull request")
	run(seed, "push", "-q", "origin", "main")
	run(primary, "fetch", "-q", "origin")

	t.Chdir(primary)
	return gitShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realGit:      realGit,
	}, primary
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
