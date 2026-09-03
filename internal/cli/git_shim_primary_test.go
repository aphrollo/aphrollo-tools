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

// The git shim is the second enforcement point for the merge-only rule: an
// agent that cannot Edit the primary checkout must not be able to move it off
// main through git either. These run against a REAL repo — the refusal reads
// git's own answers (git-dir vs common-dir, the branch, MERGE_HEAD), none of
// which an argv-echoing stub can produce truthfully.

// primaryShimRepo builds a repo on main with one linked worktree, chdirs into
// the primary checkout, and returns the two paths plus a shim config wired to
// the real git.
func primaryShimRepo(t *testing.T) (primary, linked string, cfg gitShimConfig) {
	t.Helper()
	isolateGitConfigCLI(t)
	withDirectGitShim(t)
	realGit := realGitForTest(t)
	primary = t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(primary, "init", "-q")
	run(primary, "config", "user.email", "t@example.com")
	run(primary, "config", "user.name", "t")
	run(primary, "checkout", "-q", "-B", "main")
	if err := os.WriteFile(filepath.Join(primary, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(primary, "add", "-A")
	run(primary, "commit", "-q", "-m", "init")
	run(primary, "branch", "lane/x")
	linked = filepath.Join(t.TempDir(), "lane")
	run(primary, "worktree", "add", "-q", linked, "lane/x")
	t.Chdir(primary)
	return primary, linked, gitShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realGit:      realGit,
	}
}

func currentBranch(t *testing.T, realGit, dir string) string {
	t.Helper()
	out, err := exec.Command(realGit, "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestRunGitShim_RefusesBranchCreationInThePrimaryCheckout(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)

	for _, args := range [][]string{
		{"checkout", "-b", "lane/new"},
		{"switch", "-c", "lane/new"},
		{"checkout", "lane/x"},
		{"switch", "lane/x"},
	} {
		var out, errb bytes.Buffer
		code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg)
		if code == 0 {
			t.Errorf("git %s in the primary checkout should be refused, got exit 0", strings.Join(args, " "))
		}
		if !strings.Contains(errb.String(), "primary checkout is merge-only") {
			t.Errorf("git %s: refusal must name the rule, got %q", strings.Join(args, " "), errb.String())
		}
		if !strings.Contains(errb.String(), "git worktree add -b lane/<name>") {
			t.Errorf("git %s: refusal must carry the recipe, got %q", strings.Join(args, " "), errb.String())
		}
		if b := currentBranch(t, cfg.realGit, primary); b != "main" {
			t.Fatalf("the primary checkout moved to %q — the refusal must happen before git runs", b)
		}
	}
}

func TestRunGitShim_RefusesACommitThatIsNotConcludingAMerge(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)
	if err := os.WriteFile(filepath.Join(primary, "main.go"), []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := runGitShim([]string{"commit", "-a", "-m", "direct"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("a plain commit on the primary checkout should be refused, got exit 0\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "primary checkout is merge-only") {
		t.Fatalf("refusal must name the rule, got %q", errb.String())
	}
}

func TestRunGitShim_AllowsTheCommitThatConcludesAMerge(t *testing.T) {
	_, linked, cfg := primaryShimRepo(t)
	// A real merge held open with --no-commit leaves MERGE_HEAD in place — the one state the
	// primary checkout exists for.
	writeAndCommit(t, cfg.realGit, linked, "lane.go", "package lane\n", "lane work")
	var mout, merrb bytes.Buffer
	if code := runGitShim([]string{"merge", "--no-commit", "--no-ff", "lane/x"}, strings.NewReader(""), &mout, &merrb, cfg); code != 0 {
		t.Fatalf("the merge itself must pass through, exit = %d\n%s", code, merrb.String())
	}

	var out, errb bytes.Buffer
	if code := runGitShim([]string{"commit", "-m", "merge lane/x"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("concluding a merge is exactly what the primary checkout is for, exit = %d\n%s", code, errb.String())
	}
}

// A conflicted cherry-pick and a conflicted revert leave the same situation a
// conflicted merge does, and are concluded the same way: `git commit`. Only
// MERGE_HEAD counted, so the primary checkout refused the commit that finishes
// one — with an escape (open a lane) that cannot help, because the conflicted
// state lives in THIS checkout.
func TestRunGitShim_AllowsTheCommitThatConcludesACherryPickOrRevert(t *testing.T) {
	for _, ref := range []string{"CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		t.Run(ref, func(t *testing.T) {
			primary, _, cfg := primaryShimRepo(t)
			head, err := exec.Command(cfg.realGit, "-C", primary, "rev-parse", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			// What git itself leaves mid-cherry-pick and mid-revert: the ref
			// naming the commit being applied.
			gitDirOut, err := exec.Command(cfg.realGit, "-C", primary, "rev-parse", "--git-dir").Output()
			if err != nil {
				t.Fatal(err)
			}
			gitDir := strings.TrimSpace(string(gitDirOut))
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(primary, gitDir)
			}
			if err := os.WriteFile(filepath.Join(gitDir, ref), head, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(primary, "main.go"), []byte("package main // picked\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var out, errb bytes.Buffer
			if code := runGitShim([]string{"commit", "-a", "-m", "conclude " + ref}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
				t.Fatalf("concluding a %s must pass through, exit = %d\n%s", ref, code, errb.String())
			}
		})
	}
}

func TestRunGitShim_AllowsReadingAndMergingVerbsInThePrimaryCheckout(t *testing.T) {
	_, _, cfg := primaryShimRepo(t)

	for _, args := range [][]string{
		{"status", "--porcelain"},
		{"log", "-1", "--oneline"},
		{"checkout", "main"},
		{"switch", "main"},
		{"worktree", "list"},
	} {
		var out, errb bytes.Buffer
		if code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg); code != 0 {
			t.Errorf("git %s must pass through, exit = %d\n%s", strings.Join(args, " "), code, errb.String())
		}
		if strings.Contains(errb.String(), "merge-only") {
			t.Errorf("git %s must not be refused, got %q", strings.Join(args, " "), errb.String())
		}
	}
}

func TestRunGitShim_AllowsBranchCreationInALinkedWorktree(t *testing.T) {
	_, linked, cfg := primaryShimRepo(t)
	t.Chdir(linked)

	var out, errb bytes.Buffer
	if code := runGitShim([]string{"checkout", "-b", "lane/second"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("a lane worktree is where branches are made, exit = %d\n%s", code, errb.String())
	}
}

func TestRunGitShim_PrimaryEditsEnvWaivesTheRefusal(t *testing.T) {
	_, _, cfg := primaryShimRepo(t)
	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")

	var out, errb bytes.Buffer
	if code := runGitShim([]string{"checkout", "-b", "lane/waived"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("the env escape must reach the shim too, exit = %d\n%s", code, errb.String())
	}
}

// writeAndCommit puts one file in dir and commits it with the real git.
func writeAndCommit(t *testing.T, realGit, dir, rel, body, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
