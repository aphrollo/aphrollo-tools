package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// --- isPlainMerge: pure logic, no process spawning ---------------------

// TestIsPlainMerge_TableDriven pins which `git merge` sub-forms START a
// merge (eligible for recovery) versus CONCLUDE/CANCEL one already in
// progress (never eligible -- an operator's own `merge --abort` must never
// itself get "recovered").
func TestIsPlainMerge_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		rest []string
		want bool
	}{
		{"plain merge", []string{"merge", "feature"}, true},
		{"merge --no-ff", []string{"merge", "--no-ff", "feature"}, true},
		{"merge --abort", []string{"merge", "--abort"}, false},
		{"merge --continue", []string{"merge", "--continue"}, false},
		{"merge --quit", []string{"merge", "--quit"}, false},
		{"unrelated verb", []string{"commit", "-m", "x"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPlainMerge(c.rest); got != c.want {
				t.Fatalf("isPlainMerge(%v) = %v, want %v", c.rest, got, c.want)
			}
		})
	}
}

// TestRecoverRejectedMerge_MergeAbortItself_NeverTriggers pins that
// `git merge --abort` running through the shim is never itself treated as a
// rejected merge to recover from, whatever its exit code or the marker
// state -- proven by asserting recoverRejectedMerge never even reaches the
// repo/marker/git-plumbing checks (no stderr line, code passed through
// unchanged) for an operand that would otherwise satisfy every other guard.
func TestRecoverRejectedMerge_MergeAbortItself_NeverTriggers(t *testing.T) {
	var stderr bytes.Buffer
	rest := []string{"merge", "--abort"}
	code := recoverRejectedMerge(rest, rest, "does-not-exist", "does-not-exist", 1, time.Now(), &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (unchanged)", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no recovery output for `merge --abort`, got: %s", stderr.String())
	}
}

// --- recoverRejectedMerge: real git repos, no fake stub -----------------
//
// These use the REAL git binary (resolveRealGit) against REAL temporary
// repositories: the recovery logic inspects actual git plumbing (MERGE_HEAD,
// `diff --diff-filter=U`), which a synthetic argv-echoing stub cannot
// produce truthfully.

// isolateGitConfigCLI points git's global/system config at temp files for
// the duration of t, mirroring internal/tdd's isolateGitConfig: without it,
// this box's own core.hooksPath (the aphrollo gate installed for real repo
// work) intercepts every hook this test plants, and the fixture repo's own
// setup commits recurse into a DIFFERENT, already-installed aphrollo build.
func isolateGitConfigCLI(t *testing.T) {
	t.Helper()
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

func realGitForTest(t *testing.T) string {
	t.Helper()
	bin, err := resolveRealGit()
	if err != nil {
		t.Fatalf("real git not found: %v", err)
	}
	return bin
}

// makeMergeableRepo builds two branches that merge automatically, with no
// conflict: `git merge feature` (absent any hook) would succeed cleanly.
func makeMergeableRepo(t *testing.T) (repo, branch string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) {
		if out, err := fixtureGit(append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "feature")
	run("checkout", "-q", "main")
	return repo, "feature"
}

// makeConflictingMergeRepo builds two branches that touch the SAME line of
// the same file, leaves the base branch checked out and never attempts the
// merge -- the caller drives the actual `git merge` for real.
func makeConflictingMergeRepo(t *testing.T) (repo, branch string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) {
		if out, err := fixtureGit(append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("feature change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "feature change")
	run("checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "main change")
	return repo, "feature"
}

// shellSlash renders a path for embedding in a `#!/bin/sh` hook script:
// backslashes become forward slashes, matching binShim's own shellPath
// (internal/tdd/gitgate.go) for the identical reason -- an unquoted
// backslash is a shell escape character.
func shellSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// installMarkerWritingHook plants a REAL pre-merge-commit hook that writes
// repoRoot's merge-rejected marker itself and exits 1 -- simulating the
// premergecommit gate's own rejection (internal/tdd.WriteMergeRejectedMarker)
// without depending on that code path, so this test exercises ONLY the
// shim's recovery side.
func installMarkerWritingHook(t *testing.T, repo, markerPath string) {
	t.Helper()
	hooksDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo 'REJECTED by fake hook' >&2\n" +
		"mkdir -p \"" + shellSlash(filepath.Dir(markerPath)) + "\"\n" +
		"printf '%s\\n%s\\n' \"$(date +%s)\" 'REJECTED by fake hook' > \"" + shellSlash(markerPath) + "\"\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-merge-commit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// makeAbortFailingMergeRepo builds two branches that each change DIFFERENT
// lines of the SAME file, so `git merge` auto-merges it cleanly with no
// conflict -- reproducing the exact borld shape (`.ratchet/baselines/
// module_size.txt`, re-pathed rows correct in the index) where the merge
// itself never sees a problem. filePath names the shared file relative to
// repo, for a hook to corrupt after the merge has staged it.
func makeAbortFailingMergeRepo(t *testing.T) (repo, branch, filePath string) {
	t.Helper()
	repo = t.TempDir()
	filePath = "shared.txt"
	run := func(args ...string) {
		if out, err := fixtureGit(append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(repo, filePath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("line1\nline2\nline3\n")
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-qb", "feature")
	write("line1\nline2\nline3\nfeature-line\n")
	run("add", ".")
	run("commit", "-qm", "feature")
	run("checkout", "-q", "main")
	write("main-line\nline1\nline2\nline3\n")
	run("add", ".")
	run("commit", "-qm", "main-change")
	return repo, "feature", filePath
}

// installMarkerWritingHookThatCorruptsFile plants a real pre-merge-commit
// hook that, before rejecting, appends to targetFile IN THE WORKTREE without
// touching the index -- the exact underneath-git modification the borld
// incident hit (a concurrent tightening run rewriting the baseline file
// after the merge staged it). Git's own `merge --abort` then refuses to
// discard that uncommitted change on a path the target tree also wants to
// change: "error: Entry '<path>' not uptodate. Cannot merge."
func installMarkerWritingHookThatCorruptsFile(t *testing.T, repo, markerPath, targetFile string) {
	t.Helper()
	hooksDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo 'corrupted-underneath-git' >> \"" + shellSlash(targetFile) + "\"\n" +
		"echo 'REJECTED by fake hook' >&2\n" +
		"mkdir -p \"" + shellSlash(filepath.Dir(markerPath)) + "\"\n" +
		"printf '%s\\n%s\\n' \"$(date +%s)\" 'REJECTED by fake hook' > \"" + shellSlash(markerPath) + "\"\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-merge-commit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestRunGitShim_AbortItselfFails_ReportsStillMidMerge is the #249 case: the
// recovery abort does not merely fail to help, it FAILS outright (git
// refuses to overwrite shared.txt's uncommitted worktree change), and the
// gate must say so rather than claim "aborted, checkout left clean". Both
// halves matter: the reported message names mid-merge (never the clean-abort
// line), and the checkout is verifiably STILL mid-merge (MERGE_HEAD present,
// working tree not clean) -- a session that trusted a false "clean" message
// would proceed on top of exactly this state.
func TestRunGitShim_AbortItselfFails_ReportsStillMidMerge(t *testing.T) {
	gateConfigDir(t)
	withDirectGitShim(t)
	isolateGitConfigCLI(t)
	repo, branch, filePath := makeAbortFailingMergeRepo(t)

	root := tdd.RepoRoot(repo)
	if root == "" {
		t.Fatal("setup: could not resolve the repo root")
	}
	markerPath := tdd.MergeRejectedMarkerPath(root)
	if markerPath == "" {
		t.Fatal("setup: MergeRejectedMarkerPath returned empty")
	}
	installMarkerWritingHookThatCorruptsFile(t, repo, markerPath, filePath)

	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 20 * time.Millisecond, realGit: realGitForTest(t)}
	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"-C", repo, "merge", "--no-ff", branch}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code == 0 {
		t.Fatalf("expected the fake hook's rejection to propagate as a non-zero exit, got 0\nstderr: %s", stderr.String())
	}

	if strings.Contains(stderr.String(), mergeRejectedRecoveryLine) {
		t.Fatalf("expected the abort-failed report, not the clean-abort line, got stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "STILL MID-MERGE") {
		t.Fatalf("expected the reported message to say the checkout is still mid-merge, got stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), filePath) {
		t.Fatalf("expected the reported message to name %q (the path git named), got stderr:\n%s", filePath, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("expected MERGE_HEAD to survive a failed abort (checkout still mid-merge), stat err = %v", err)
	}
	statusOut, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(statusOut)) == "" {
		t.Fatalf("expected a NOT-clean checkout after a failed abort, but git status --porcelain was empty")
	}
}

// TestRunGitShim_MergeRejectedByHook_AbortsAndCleansCheckout is the literal
// required case: a real pre-merge-commit hook rejects an otherwise-clean
// automerge and writes the marker (simulating the actual gate's own
// rejection) -- after runGitShim, MERGE_HEAD is gone, the working tree is
// clean, and the one recovery line was printed.
func TestRunGitShim_MergeRejectedByHook_AbortsAndCleansCheckout(t *testing.T) {
	gateConfigDir(t)
	withDirectGitShim(t)
	isolateGitConfigCLI(t)
	repo, branch := makeMergeableRepo(t)

	root := tdd.RepoRoot(repo)
	if root == "" {
		t.Fatal("setup: could not resolve the repo root")
	}
	markerPath := tdd.MergeRejectedMarkerPath(root)
	if markerPath == "" {
		t.Fatal("setup: MergeRejectedMarkerPath returned empty")
	}
	installMarkerWritingHook(t, repo, markerPath)

	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 20 * time.Millisecond, realGit: realGitForTest(t)}
	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"-C", repo, "merge", "--no-ff", branch}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code == 0 {
		t.Fatalf("expected the fake hook's rejection to propagate as a non-zero exit, got 0\nstderr: %s", stderr.String())
	}

	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("expected MERGE_HEAD to be gone after recovery (stat err = %v)", err)
	}
	statusOut, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Fatalf("expected a clean checkout after recovery, git status --porcelain:\n%s", statusOut)
	}
	if !strings.Contains(stderr.String(), mergeRejectedRecoveryLine) {
		t.Fatalf("expected the recovery line %q, got stderr:\n%s", mergeRejectedRecoveryLine, stderr.String())
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("expected the marker to be removed after recovery (stat err = %v)", err)
	}
}

// TestRecoverRejectedMerge_ConflictedMerge_MarkerPresent_NotAborted pins the
// safety limit: even with a FRESH marker present, a REAL conflict (both
// branches touch the same line) must never be auto-aborted -- the operator
// must resolve it by hand. The marker is written directly (a real
// conflicting merge never invokes pre-merge-commit at all, so no hook could
// write one) with an mtime after `start`, isolating the one guard this test
// exists to prove: `hasUnmergedPaths`, not marker freshness.
func TestRecoverRejectedMerge_ConflictedMerge_MarkerPresent_NotAborted(t *testing.T) {
	gateConfigDir(t)
	isolateGitConfigCLI(t)
	repo, branch := makeConflictingMergeRepo(t)
	realGit := realGitForTest(t)

	start := time.Now()

	mergeCmd := fixtureGit("-C", repo, "merge", branch)
	mergeOut, mergeErr := mergeCmd.CombinedOutput()
	if mergeErr == nil {
		t.Fatalf("setup: expected `git merge %s` to conflict, but it succeeded:\n%s", branch, mergeOut)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("setup: expected MERGE_HEAD after a real conflict, stat err = %v", err)
	}

	root := tdd.RepoRoot(repo)
	if root == "" {
		t.Fatal("setup: could not resolve the repo root")
	}
	tdd.WriteMergeRejectedMarker(root, "REJECTED (simulated)")
	markerPath := tdd.MergeRejectedMarkerPath(root)
	if info, err := os.Stat(markerPath); err != nil || info.ModTime().Before(start) {
		t.Fatalf("setup: marker must exist and be no older than start (err=%v)", err)
	}

	var stderr bytes.Buffer
	code := recoverRejectedMerge([]string{"merge", branch}, []string{"-C", repo, "merge", branch}, repo, realGit, 1, start, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (unchanged)", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no recovery output for a real conflict, got: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("expected MERGE_HEAD to survive (a real conflict is never auto-aborted), stat err = %v", err)
	}
}

// TestRecoverRejectedMerge_FailsWithoutMarker_NotAborted pins the other
// safety limit: a merge that fails for a reason the gate never touched (here
// simulating `fatal: refusing to merge unrelated histories`, exit 128, no
// hook ever ran) leaves no marker, so recovery must never fire.
func TestRecoverRejectedMerge_FailsWithoutMarker_NotAborted(t *testing.T) {
	gateConfigDir(t)
	isolateGitConfigCLI(t)
	repo, _ := makeMergeableRepo(t) // a real repo; no merge attempted, no marker ever written
	realGit := realGitForTest(t)

	var stderr bytes.Buffer
	code := recoverRejectedMerge([]string{"merge", "other/other"}, []string{"-C", repo, "merge", "other/other"}, repo, realGit, 128, time.Now(), &stderr)
	if code != 128 {
		t.Fatalf("code = %d, want 128 (unchanged)", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no recovery output when there is no marker, got: %s", stderr.String())
	}
}
