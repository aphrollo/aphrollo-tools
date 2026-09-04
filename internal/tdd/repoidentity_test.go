package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// initRepoWithCommit makes a real repository with one commit, so its root
// commit — the thing repoIdentity is built from — actually exists.
func initRepoWithCommit(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		if out, err := git(root, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-qm", "first"}} {
		if out, err := git(root, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

// TestRepoIdentity_IsTheSameInACloneAsInTheOriginal is the whole point of the
// identity: a run measured in a Linux-side clone and a merge happening in the
// Windows checkout are the same repository, and must agree even though the
// two directories share no path spelling at all.
func TestRepoIdentity_IsTheSameInACloneAsInTheOriginal(t *testing.T) {
	origin := initRepoWithCommit(t, "hello")
	clone := filepath.Join(t.TempDir(), "clone")
	if out, err := git(t.TempDir(), "clone", "-q", origin, clone); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}

	want, got := repoIdentity(origin), repoIdentity(clone)
	if want == "" {
		t.Fatal("repoIdentity named the original repository the empty string")
	}
	if got != want {
		t.Errorf("repoIdentity(clone) = %q, want the original's %q", got, want)
	}
}

// TestRepoIdentity_DiffersBetweenTwoUnrelatedRepositories proves the identity
// still separates repositories: two repos with unrelated histories must never
// share one, or a receipt from anywhere would merge anywhere.
func TestRepoIdentity_DiffersBetweenTwoUnrelatedRepositories(t *testing.T) {
	a := repoIdentity(initRepoWithCommit(t, "a"))
	b := repoIdentity(initRepoWithCommit(t, "b"))
	if a == "" || b == "" {
		t.Fatalf("repoIdentity returned empty: a=%q b=%q", a, b)
	}
	if a == b {
		t.Errorf("two unrelated repositories share the identity %q", a)
	}
}

// TestRepoIdentity_IsEmptyOutsideARepository pins the fallback's trigger: with
// no git answer there is no identity, and judgeReceiptRepo then falls back to
// comparing paths rather than treating "" as a match with everything.
func TestRepoIdentity_IsEmptyOutsideARepository(t *testing.T) {
	if got := repoIdentity(t.TempDir()); got != "" {
		t.Errorf("repoIdentity(non-repo) = %q, want the empty string", got)
	}
}
