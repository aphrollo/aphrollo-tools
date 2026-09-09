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

// TestSync_RefusesDirtyWorktree used to dirty an UNRELATED tracked file
// (base.txt) while origin advanced a different one (other.txt) and expect a
// refusal — that was the old pre-check's behavior: refuse on ANY dirty
// tracked file, regardless of whether the incoming commits touched it. Git's
// own --ff-only refuses only when the fast-forward would overwrite the dirty
// path itself, so this now dirties the SAME file the update advances, to keep
// exercising a genuine refusal under the new rule.
func TestSync_RefusesDirtyWorktree(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "base.txt", "origin base\n")
	// Leave an uncommitted, conflicting change to the same file.
	dirtyContent := []byte("dirty\n")
	if err := os.WriteFile(filepath.Join(clone, "base.txt"), dirtyContent, 0o644); err != nil {
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
	if !strings.Contains(out.String(), "could not fast-forward") {
		t.Errorf("refusal should name the failed fast-forward:\n%s", out.String())
	}
	// Fetch still happened: origin/main is the advanced tip.
	if revOf(t, clone, "refs/remotes/origin/main") == headBefore {
		t.Errorf("sync should still fetch origin even when it refuses the fast-forward")
	}
	gotContent, err := os.ReadFile(filepath.Join(clone, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotContent, dirtyContent) {
		t.Errorf("a refused sync must not touch the uncommitted content: got %q, want %q", gotContent, dirtyContent)
	}
}

// TestSync_FastForwardsPastADirtyFileTheUpdateDoesNotTouch: an uncommitted
// change to a file the incoming commits never touch must not block the
// fast-forward — git's own --ff-only only cares about paths the merge would
// actually overwrite.
func TestSync_FastForwardsPastADirtyFileTheUpdateDoesNotTouch(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "other.txt", "other\n") // origin's update touches other.txt only
	if err := os.WriteFile(filepath.Join(clone, "base.txt"), []byte("dirty base\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("Sync: %v\n%s", err, errb.String())
	}
	if revOf(t, clone, "HEAD") != revOf(t, clone, "refs/remotes/origin/main") {
		t.Errorf("a dirty file the update does not touch should still fast-forward")
	}
	got, err := os.ReadFile(filepath.Join(clone, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dirty base\n" {
		t.Errorf("the dirty file should keep its uncommitted content, got %q", got)
	}
	want := "fast-forwarded main to origin/main (1 commit(s))"
	if !strings.Contains(out.String(), want) {
		t.Errorf("stdout should report the fast-forward:\ngot:  %s\nwant substring: %s", out.String(), want)
	}
}

// TestSync_LeavesATreeWhoseDirtyFileTheUpdateTouches: an uncommitted change
// to a file the incoming commits DO touch is exactly what git's --ff-only
// refuses — sync reports git's own reason and leaves the tree untouched
// (non-destructive, exit 0).
func TestSync_LeavesATreeWhoseDirtyFileTheUpdateTouches(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "base.txt", "origin base\n") // origin's update touches base.txt
	dirtyContent := []byte("dirty base\n")
	if err := os.WriteFile(filepath.Join(clone, "base.txt"), dirtyContent, 0o644); err != nil {
		t.Fatal(err)
	}

	headBefore := revOf(t, clone, "HEAD")
	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("a conflicting-dirty clone must NOT error — best-effort, exit 0: %v\n%s", err, errb.String())
	}
	if revOf(t, clone, "HEAD") != headBefore {
		t.Errorf("a refused fast-forward must not move HEAD")
	}
	if !strings.Contains(out.String(), "main could not fast-forward: ") {
		t.Errorf("stdout should report the refusal:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "would be overwritten by merge") {
		t.Errorf("stdout should carry git's own reason:\n%s", out.String())
	}
	gotContent, err := os.ReadFile(filepath.Join(clone, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotContent, dirtyContent) {
		t.Errorf("a refused fast-forward must not touch the uncommitted content: got %q, want %q", gotContent, dirtyContent)
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

// TestSyncReasonLine_PrefersTheErrorLineOverProgress: git's ff-only failure
// writes `Updating a..b` to stdout and its actual reason (`error: ...` /
// `fatal: ...`) to stderr; CombinedOutput only happens to interleave the
// error first through stdio buffering, so reasonLine must pick the
// diagnostic line explicitly rather than trust output order.
func TestSyncReasonLine_PrefersTheErrorLineOverProgress(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "error line after progress noise",
			in:   "Updating ab12..cd34\nerror: Your local changes to the following files would be overwritten by merge:\n\ta.txt\n",
			want: "error: Your local changes to the following files would be overwritten by merge:\na.txt",
		},
		{
			name: "fatal line alone",
			in:   "fatal: not a git repository\n",
			want: "fatal: not a git repository",
		},
		{
			name: "no error/fatal line falls back to first non-empty line",
			in:   "Updating ab12..cd34\n",
			want: "Updating ab12..cd34",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reasonLine([]byte(c.in)); got != c.want {
				t.Errorf("reasonLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestReasonLine_KeepsThePathGitNamedNotJustTheSentence: git's real refusal
// output puts the offending path on a tab-indented line right after the
// error: sentence — dropping it (the old behavior) leaves the user with the
// generic sentence and no idea which file to look at.
func TestReasonLine_KeepsThePathGitNamedNotJustTheSentence(t *testing.T) {
	// Real git ff-only refusal output, verbatim.
	out := "error: Your local changes to the following files would be overwritten by merge:\n\tbase.txt\nPlease commit your changes or stash them before you merge.\nAborting\n"

	got := reasonLine([]byte(out))
	if !strings.Contains(got, "base.txt") {
		t.Errorf("reasonLine(%q) = %q, want it to contain the path %q git named", out, got, "base.txt")
	}
}

// TestSync_ReturnsAnErrorWhenGitFailsForAnotherReason: a `git merge --ff-only`
// failure that is NOT a dirty-path refusal — here, the object git needs to
// write into the worktree is missing from the local object store (corrupted
// clone, interrupted transfer) — must surface as a real error, not the
// silent "could not fast-forward" report the refusal case gets. HEAD must
// still not move. (A stale .git/index.lock was tried first: this box's git
// queue shim treats that file as a live-contention signal and waits on it
// forever, so it is not a usable fixture here — a missing object is
// deterministic and does not touch that shim's own lock detection.)
func TestSync_ReturnsAnErrorWhenGitFailsForAnotherReason(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "other.txt", "other\n")

	// Fetch now so the incoming blob is in the local object store, then
	// delete its loose object file: the fast-forward needs that content to
	// populate the worktree, and a missing object fails for a reason that has
	// nothing to do with a dirty path.
	gitRun(t, clone, "fetch", "-q", "origin")
	blobOut, err := exec.Command("git", "-C", clone, "rev-parse", "origin/main:other.txt").Output()
	if err != nil {
		t.Fatalf("rev-parse origin/main:other.txt: %v", err)
	}
	blob := strings.TrimSpace(string(blobOut))
	objPath := filepath.Join(clone, ".git", "objects", blob[:2], blob[2:])
	if err := os.Remove(objPath); err != nil {
		t.Fatalf("remove blob object %s: %v", objPath, err)
	}

	headBefore := revOf(t, clone, "HEAD")
	var out, errb bytes.Buffer
	err = Sync(clone, false, &out, &errb)
	if err == nil {
		t.Fatalf("a git failure that is not a dirty-path refusal should return an error, stdout:\n%s", out.String())
	}
	if revOf(t, clone, "HEAD") != headBefore {
		t.Errorf("a failed merge must not move HEAD")
	}
}

// TestSync_DefaultsToTheCwdRepo: an empty repoArg resolves the caller's cwd
// repo, the same rule commit uses (ResolveTarget("","","")), rather than
// erroring on a missing positional.
func TestSync_DefaultsToTheCwdRepo(t *testing.T) {
	clone := repoWithOrigin(t)
	advanceOrigin(t, clone, "other.txt", "other\n")
	t.Chdir(clone)

	var out, errb bytes.Buffer
	if err := Sync("", false, &out, &errb); err != nil {
		t.Fatalf("Sync: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "fast-forward") {
		t.Errorf("cwd-resolved sync should report a fast-forward:\n%s", out.String())
	}
	if revOf(t, clone, "refs/heads/main") != revOf(t, clone, "refs/remotes/origin/main") {
		t.Errorf("local main should have advanced to origin/main")
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

// TestSync_NeverStrandsTheCheckoutHoldingTheDefaultBranch: the ref-only path
// (`git update-ref refs/heads/<def> origin/<def>`) moves a ref that is SHARED
// by every worktree of the repo, so it fires whenever the checkout sync was
// pointed at is not itself on the default branch — including when another
// worktree is. That checkout is then left past its own HEAD: its index and
// files still hold the pre-merge content, so `git status` there shows the
// commit that just landed as a STAGED REVERT (a `D <path>` for every file the
// merge added), and the next commit made in it silently undoes the merge.
// Issue #618, observed on the box after `workspace merge` landed PR #616.
//
// The invariant, whichever way the fix goes: the checkout that holds the
// default branch is never left with a staged difference from its own HEAD, and
// what stdout claims about the ref matches what the ref actually did.
func TestSync_NeverStrandsTheCheckoutHoldingTheDefaultBranch(t *testing.T) {
	clone := repoWithOrigin(t)
	// The clone moves off main and a linked worktree takes it — so sync's
	// "default branch is not checked out HERE" ref-only path is what runs,
	// while a real checkout is sitting on the branch it moves.
	gitRun(t, clone, "checkout", "-q", "-b", "feat/elsewhere")
	holder := filepath.Join(t.TempDir(), "holder")
	gitRun(t, clone, "worktree", "add", "-q", holder, "main")
	advanceOrigin(t, clone, "other.txt", "other\n")

	var out, errb bytes.Buffer
	if err := Sync(clone, false, &out, &errb); err != nil {
		t.Fatalf("Sync: %v\n%s", err, errb.String())
	}

	// STATE: no staged revert. An empty porcelain status is the whole point —
	// the checkout's index and files agree with the HEAD it now reports.
	st, err := exec.Command("git", "-C", holder, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status in the holding worktree: %v", err)
	}
	if strings.TrimSpace(string(st)) != "" {
		t.Errorf("the checkout holding main was left with uncommitted state — a staged revert of what just synced:\n%s\nstdout:\n%s", st, out.String())
	}

	// FACT: stdout's claim must match the ref. A reader must be able to tell
	// "the checkout is at the new tip" from "the ref moved and it is not"
	// without running a git command.
	moved := revOf(t, clone, "refs/heads/main") == revOf(t, clone, "refs/remotes/origin/main")
	claimed := strings.Contains(out.String(), "fast-forwarded main")
	if claimed != moved {
		t.Errorf("stdout claims a fast-forward=%v but refs/heads/main moved=%v:\n%s", claimed, moved, out.String())
	}
	if moved {
		if _, err := os.Stat(filepath.Join(holder, "other.txt")); err != nil {
			t.Errorf("main advanced, so the checkout holding it must have the new file: %v", err)
		}
	}
}
