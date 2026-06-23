package workspace

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// branchAhead creates feat/diff off the current HEAD with one extra commit that
// adds a file, and returns the branch name. The repo's default branch stays put,
// so the branch is one commit ahead of it — exactly what a PR diff shows.
func branchAhead(t *testing.T, repo string) string {
	t.Helper()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/diff")
	writeFile(t, repo, "added.txt", "new line\n")
	run("add", ".")
	run("commit", "-q", "-m", "add added.txt")
	return "feat/diff"
}

func TestDiff_AgainstDefaultBranch(t *testing.T) {
	repo := initRepo(t)
	branch := branchAhead(t, repo)

	var out, errb bytes.Buffer
	if err := Diff(targetFor(repo, branch), false, &out, &errb); err != nil {
		t.Fatalf("Diff: %v\n%s", err, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "added.txt") || !strings.Contains(got, "+new line") {
		t.Errorf("diff should show the branch's added file verbatim:\n%s", got)
	}
}

func TestDiff_LocalDefaultFallbackNoOrigin(t *testing.T) {
	// initRepo has NO origin, so diffBase must fall back to the LOCAL default
	// branch (main). Advance local main after branching: the three-dot diff base
	// is the merge-base, so the branch's own commit shows and main's later commit
	// does not — proving the diff is taken against local main, not a missing origin.
	repo := initRepo(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/diff")
	writeFile(t, repo, "added.txt", "branch line\n")
	run("add", ".")
	run("commit", "-q", "-m", "branch commit")
	// Advance local main with an unrelated commit the branch never gained.
	run("checkout", "-q", "main")
	writeFile(t, repo, "mainonly.txt", "main only\n")
	run("add", ".")
	run("commit", "-q", "-m", "main commit")
	run("checkout", "-q", "feat/diff")

	var out, errb bytes.Buffer
	if err := Diff(targetFor(repo, "feat/diff"), false, &out, &errb); err != nil {
		t.Fatalf("Diff: %v\n%s", err, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "added.txt") || !strings.Contains(got, "+branch line") {
		t.Errorf("local-default fallback should show the branch's commit:\n%s", got)
	}
	if strings.Contains(got, "mainonly.txt") {
		t.Errorf("three-dot base is the merge-base; main's later commit must not appear:\n%s", got)
	}
}

func TestDiff_Stat(t *testing.T) {
	repo := initRepo(t)
	branch := branchAhead(t, repo)

	var out, errb bytes.Buffer
	if err := Diff(targetFor(repo, branch), true, &out, &errb); err != nil {
		t.Fatalf("Diff --stat: %v\n%s", err, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "added.txt") {
		t.Errorf("--stat should name the changed file:\n%s", got)
	}
	// A diffstat summarises; it must NOT contain the raw added line.
	if strings.Contains(got, "+new line") {
		t.Errorf("--stat should be a diffstat, not the full diff:\n%s", got)
	}
}

func TestDiff_EmptyExitsZero(t *testing.T) {
	repo := initRepo(t)
	// No branch ahead — HEAD == default branch, so the diff is empty. It must
	// still succeed (exit 0), printing nothing.
	var out, errb bytes.Buffer
	if err := Diff(targetFor(repo, "main"), false, &out, &errb); err != nil {
		t.Fatalf("empty diff should not error: %v\n%s", err, errb.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("empty diff should print nothing, got:\n%s", out.String())
	}
}
