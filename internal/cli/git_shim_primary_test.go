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

// A fast-forward merge, a fast-forward pull, a cherry-pick, a rebase or a
// `reset --hard` onto another ref all move main with no premergecommit hook
// firing at all — the same hole a branch-creating checkout opens, just
// through five other doors (issue #116).
func TestRunGitShim_RefusesTheVerbsThatBypassThePrimaryHook(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)
	initSHA := strings.TrimSpace(mustOutput(t, cfg.realGit, primary, "rev-parse", "HEAD"))

	for _, args := range [][]string{
		{"merge", "lane/x"},
		{"pull", "origin", "main"},
		{"cherry-pick", initSHA},
		{"rebase", "lane/x"},
		{"reset", "--hard", "lane/x"},
	} {
		var out, errb bytes.Buffer
		code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg)
		if code == 0 {
			t.Errorf("git %s in the primary checkout should be refused, got exit 0", strings.Join(args, " "))
		}
		if !strings.Contains(errb.String(), "primary checkout is merge-only") {
			t.Errorf("git %s: refusal must name the rule, got %q", strings.Join(args, " "), errb.String())
		}
		if b := currentBranch(t, cfg.realGit, primary); b != "main" {
			t.Fatalf("git %s moved the primary checkout to %q — the refusal must happen before git runs",
				strings.Join(args, " "), b)
		}
	}
}

// The refusal is narrow: it exists to make sure the hook fires, not to trap
// an operator resolving a conflict already in progress, or discarding
// uncommitted changes without moving anywhere.
func TestRunGitShim_AllowsTheSequencerConcludeVerbsAndAHarmlessReset(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)

	for _, args := range [][]string{
		{"cherry-pick", "--abort"},
		{"cherry-pick", "--continue"},
		{"cherry-pick", "--quit"},
		{"rebase", "--abort"},
		{"rebase", "--continue"},
		{"rebase", "--skip"},
		{"reset", "--hard"},
		{"reset"},
		{"reset", "--", "main.go"},
	} {
		var out, errb bytes.Buffer
		runGitShim(args, strings.NewReader(""), &out, &errb, cfg)
		if strings.Contains(errb.String(), "merge-only") {
			t.Errorf("git %s must not be refused, got %q", strings.Join(args, " "), errb.String())
		}
	}
	if b := currentBranch(t, cfg.realGit, primary); b != "main" {
		t.Fatalf("primary checkout moved to %q — none of these reset forms name a ref to move to", b)
	}
}

// A `reset` that names a ref moves main's tip to it regardless of mode —
// only the treatment of the index and working tree differs between
// --soft/--mixed(default)/--hard/--merge/--keep. Refusing only the --hard
// form left the other four as an open door onto the exact hole the wall
// exists to close: main's tip landing somewhere with no premergecommit hook
// firing at all.
func TestRunGitShim_RefusesAResetToAnotherRefRegardlessOfMode(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)
	beforeSHA := strings.TrimSpace(mustOutput(t, cfg.realGit, primary, "rev-parse", "HEAD"))

	for _, args := range [][]string{
		{"reset", "lane/x"},
		{"reset", "--soft", "lane/x"},
		{"reset", "--merge", "lane/x"},
		{"reset", "--keep", "lane/x"},
	} {
		var out, errb bytes.Buffer
		code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg)
		if code == 0 {
			t.Errorf("git %s in the primary checkout should be refused, got exit 0", strings.Join(args, " "))
		}
		if !strings.Contains(errb.String(), "primary checkout is merge-only") {
			t.Errorf("git %s: refusal must name the rule, got %q", strings.Join(args, " "), errb.String())
		}
		afterSHA := strings.TrimSpace(mustOutput(t, cfg.realGit, primary, "rev-parse", "HEAD"))
		if afterSHA != beforeSHA {
			t.Fatalf("git %s moved main from %s to %s — the refusal must happen before git runs",
				strings.Join(args, " "), beforeSHA, afterSHA)
		}
	}
}

// `--no-ff` is the escape that keeps the merge (or pull) itself passing
// through: it is what makes the resulting commit real rather than a
// fast-forward, so the premergecommit hook fires on it.
func TestRunGitShim_AllowsAMergeOrPullThatCannotFastForward(t *testing.T) {
	_, linked, cfg := primaryShimRepo(t)
	writeAndCommit(t, cfg.realGit, linked, "lane.go", "package lane\n", "lane work")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"merge", "--no-ff", "--no-commit", "lane/x"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("git merge --no-ff should pass through, exit = %d\n%s", code, errb.String())
	}
	if strings.Contains(errb.String(), "merge-only") {
		t.Fatalf("git merge --no-ff must not be refused, got %q", errb.String())
	}
}

// mustOutput runs git and hands back its stdout, failing the test on error.
func mustOutput(t *testing.T, realGit, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command(realGit, append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
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

// A repo alias that expands to a refused verb must not walk past the wall
// just because primaryRefusedVerb only ever saw the literal alias name
// (issue #279): `[alias] cob = checkout -b` is exactly `checkout -b` once
// git itself expands it, and the primary checkout must refuse it the same
// way it refuses `checkout -b` spelled out.
func TestRunGitShim_RefusesAnAliasThatExpandsToBranchCreation(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)
	cmd := exec.Command(cfg.realGit, "-C", primary, "config", "alias.cob", "checkout -b")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config alias.cob: %v\n%s", err, out)
	}

	var out, errb bytes.Buffer
	code := runGitShim([]string{"cob", "lane/new"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("git cob lane/new (alias for checkout -b) should be refused, got exit 0")
	}
	if !strings.Contains(errb.String(), "primary checkout is merge-only") {
		t.Fatalf("refusal must name the rule, got %q", errb.String())
	}
	if b := currentBranch(t, cfg.realGit, primary); b != "main" {
		t.Fatalf("the primary checkout moved to %q via an alias — the refusal must happen before git runs", b)
	}
}

// A shell (`!`-prefixed) alias's expansion is arbitrary shell, not a git
// verb sequence, so it cannot be classified by primaryRefusedVerb at all.
// The primary checkout must refuse it outright rather than let it through
// for lack of a recognized verb (issue #279): a marker file the shell
// command would create must never appear, proving the shell alias never ran.
func TestRunGitShim_RefusesAShellAliasOutrightInThePrimaryCheckout(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)
	marker := filepath.Join(primary, "shell-alias-ran")
	aliasCmd := "!touch " + strings.ReplaceAll(marker, `\`, `/`)
	cmd := exec.Command(cfg.realGit, "-C", primary, "config", "alias.pwn", aliasCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config alias.pwn: %v\n%s", err, out)
	}

	var out, errb bytes.Buffer
	code := runGitShim([]string{"pwn"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("git pwn (a shell alias) in the primary checkout should be refused, got exit 0")
	}
	if !strings.Contains(errb.String(), "primary checkout is merge-only") {
		t.Fatalf("refusal must name the rule, got %q", errb.String())
	}
	if !strings.Contains(errb.String(), "shell alias") {
		t.Fatalf("refusal must say why it could not classify the invocation, got %q", errb.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the shell alias ran (marker file exists) — it must be refused before git executes it")
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
