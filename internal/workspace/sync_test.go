package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// revOf returns the SHA a ref resolves to in repo (test assertion helper).
func revOf(t *testing.T, repo, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

func TestSync_AlreadyCurrent(t *testing.T) {
	clone := repoWithOrigin(t) // on main, level with origin/main

	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("Sync: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "already current") && !strings.Contains(out.String(), "[skip]") {
		t.Errorf("an up-to-date clone should be a no-op skip:\n%s", out.String())
	}
}

func TestSync_FastForwardsCleanOnDefault(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "other.txt", "other\n") // origin/main moves ahead

	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("Sync: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "fast-forward") {
		t.Errorf("a behind clone should report a fast-forward:\n%s", out.String())
	}
	// Local main now equals origin/main, and the new file is in the working tree.
	if revOf(t, clone, "refs/heads/main") != revOf(t, clone, "refs/remotes/origin/main") {
		t.Errorf("local main should have advanced to origin/main")
	}
	if _, err := os.Stat(filepath.Join(clone, "other.txt")); err != nil {
		t.Errorf("fast-forward should have brought origin's commit into the worktree: %v", err)
	}
}

func TestSync_RefusesDirtyWorktree(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "other.txt", "other\n")
	// Leave an uncommitted change so the default worktree is dirty.
	if err := os.WriteFile(filepath.Join(clone, "base.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	headBefore := revOf(t, clone, "HEAD")
	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("a dirty clone must NOT error — best-effort, exit 0: %v\n%s", err, errb.String())
	}
	if revOf(t, clone, "HEAD") != headBefore {
		t.Errorf("a refused sync must not move HEAD")
	}
	if !strings.Contains(out.String(), "uncommitted") && !strings.Contains(out.String(), "untouched") {
		t.Errorf("refusal should name the dirty state:\n%s", out.String())
	}
	// Fetch still happened: origin/main is the advanced tip.
	if revOf(t, clone, "refs/remotes/origin/main") == headBefore {
		t.Errorf("sync should still fetch origin even when it refuses the fast-forward")
	}
}

func TestSync_RefusesDiverged(t *testing.T) {
	clone := repoWithOrigin(t)
	// A local commit on main that origin does not have...
	if err := os.WriteFile(filepath.Join(clone, "local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "add", ".")
	gitRun(t, clone, "commit", "-q", "-m", "local main commit")
	// ...and origin advances independently => the branches diverge.
	advanceOrigin(t, clone, "other.txt", "other\n")

	headBefore := revOf(t, clone, "HEAD")
	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("a diverged clone must NOT error — best-effort, exit 0: %v\n%s", err, errb.String())
	}
	if revOf(t, clone, "HEAD") != headBefore {
		t.Errorf("a diverged sync must not move the local default branch")
	}
	if !strings.Contains(out.String(), "diverged") {
		t.Errorf("refusal should explain the divergence:\n%s", out.String())
	}
}

func TestSync_DefaultNotCheckedOut(t *testing.T) {
	clone := repoWithOrigin(t)
	// HEAD moves off the default branch (as if a sibling worktree is mid-ticket).
	gitRun(t, clone, "checkout", "-q", "-b", "feat/elsewhere")
	advanceOrigin(t, clone, "other.txt", "other\n")

	headBefore := revOf(t, clone, "HEAD")
	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("Sync: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "fast-forward") {
		t.Errorf("an off-default clone should still fast-forward the main ref:\n%s", out.String())
	}
	// The default branch ref advanced to origin without touching HEAD.
	if revOf(t, clone, "refs/heads/main") != revOf(t, clone, "refs/remotes/origin/main") {
		t.Errorf("refs/heads/main should have advanced to origin/main")
	}
	if revOf(t, clone, "HEAD") != headBefore {
		t.Errorf("syncing the default ref must not move HEAD off the current branch")
	}
}

func TestSync_DryMutatesNothing(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "other.txt", "other\n")

	headBefore := revOf(t, clone, "HEAD")
	var out, errb bytes.Buffer
	if err := Sync(clone, true, &out, &errb); err != nil {
		t.Fatalf("dry Sync: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "would fast-forward") {
		t.Errorf("dry run should preview the fast-forward:\n%s", out.String())
	}
	if revOf(t, clone, "refs/heads/main") != headBefore {
		t.Errorf("dry run must not move the local default branch")
	}
	if _, err := os.Stat(filepath.Join(clone, "other.txt")); !os.IsNotExist(err) {
		t.Errorf("dry run must not change the working tree")
	}
}

func TestSync_RemotelessIsNonFatal(t *testing.T) {
	repo := initRepo(t) // a plain repo, no origin remote

	var out, errb bytes.Buffer
	if err := Sync(repo, false, &out, &errb); err != nil {
		t.Fatalf("a remote-less repo must not error (offline is non-fatal): %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "[skip]") {
		t.Errorf("nothing to fast-forward toward should be a clean skip:\n%s", out.String())
	}
}
