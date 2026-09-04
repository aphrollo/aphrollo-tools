package ratchet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGitConfigRatchet points git's global/system config at temp files
// for the duration of t: without it, this box's own core.hooksPath (the
// aphrollo gate installed for real repo work) intercepts every git
// operation these fixtures run, in a throwaway t.TempDir() repo that has
// nothing to do with the gate.
func isolateGitConfigRatchet(t *testing.T) {
	t.Helper()
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

// gitRun runs `git -C dir <args>` for test setup, failing the test loudly on
// error -- setup is not the thing under test, so any failure here is a bad
// fixture, not a finding.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// repoWithNanGuardMidMerge builds repoWithNanGuard's tree (check_test.go) as
// a REAL git repo carrying a genuine, unresolved conflicting merge -- two
// branches that touch the same line of the same file, so `git merge` leaves
// MERGE_HEAD in place exactly the way a rejected pre-merge-commit hook does.
// The conflict lives entirely in conflict.txt, deliberately unrelated to the
// nan-guard fixture files: MERGE_HEAD's presence must gate tightening no
// matter what the conflict itself is about.
func repoWithNanGuardMidMerge(t *testing.T) string {
	t.Helper()
	root := repoWithNanGuard(t)
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	write(t, filepath.Join(root, "conflict.txt"), "base\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")
	gitRun(t, root, "checkout", "-qb", "feature")
	write(t, filepath.Join(root, "conflict.txt"), "feature change\n")
	gitRun(t, root, "add", "conflict.txt")
	gitRun(t, root, "commit", "-qm", "feature change")
	gitRun(t, root, "checkout", "-q", "main")
	write(t, filepath.Join(root, "conflict.txt"), "main change\n")
	gitRun(t, root, "add", "conflict.txt")
	gitRun(t, root, "commit", "-qm", "main change")

	merge := exec.Command("git", "-C", root, "merge", "feature")
	if out, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("setup: expected `git merge feature` to conflict, but it succeeded:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("setup: expected MERGE_HEAD after a real conflict, stat err = %v", err)
	}
	return root
}

// TestCheckRefusesToTightenWhileMergeHeadIsPresent_BaselineUntouched is the
// #248 case: a tightening run scanning DURING a real, unresolved merge must
// not write the baseline, even though the scan sees the nan-guard site as
// fixed (which, absent a merge, is exactly what TIGHTENS it -- see
// TestCheckTightensTheBaselineWhenASiteIsFixed). Refusing entirely, not just
// declining to record THIS site, is the fix: a tree mid-merge is not a tree
// any commit will ever equal, so nothing measured against it belongs in the
// baseline.
func TestCheckRefusesToTightenWhileMergeHeadIsPresent_BaselineUntouched(t *testing.T) {
	root := repoWithNanGuardMidMerge(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")
	baselinePath := filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")
	before := read(t, baselinePath)

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Tightened) != 0 {
		t.Fatalf("expected no law to tighten while MERGE_HEAD is present, got %v", res.Tightened)
	}
	after := read(t, baselinePath)
	if after != before {
		t.Errorf("baseline changed while mid-merge: before %q, after %q", before, after)
	}
}

// TestCheckTightensNormallyOnceMergeConcludes_NoMergeHeadPresent is the
// companion proof: the SAME repo, once the merge concludes (here, aborted),
// tightens exactly as TestCheckTightensTheBaselineWhenASiteIsFixed expects
// -- the refusal is scoped to MERGE_HEAD's presence, not a standing change
// in behavior.
func TestCheckTightensNormallyOnceMergeConcludes_NoMergeHeadPresent(t *testing.T) {
	root := repoWithNanGuardMidMerge(t)
	gitRun(t, root, "merge", "--abort")
	if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("setup: expected MERGE_HEAD gone after abort, stat err = %v", err)
	}
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Tightened) != 1 {
		t.Fatalf("expected the nan-guard baseline to tighten once no merge is in progress, got %v", res.Tightened)
	}
	got := read(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))
	if got != "# one known site\n" {
		t.Errorf("baseline = %q — the fixed site must be dropped, the header kept", got)
	}
}
