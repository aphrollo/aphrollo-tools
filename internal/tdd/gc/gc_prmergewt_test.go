package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// deadPidForTest returns a pid guaranteed to name no running process: a
// child this test starts and waits for to exit. Reused rather than a large
// made-up number, which risks colliding with a real process on a loaded box.
func deadPidForTest(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running a throwaway child: %v", err)
	}
	return cmd.Process.Pid
}

// writeHolder writes wt's holder record with the given pid, matching the
// format prGateWriteHolder (internal/tdd/merge/premergepr.go) writes.
func writeHolder(t *testing.T, wt string, pid int) {
	t.Helper()
	mkFile(t, filepath.Join(wt, gatePRMergeHolderFile),
		"pid="+strconv.Itoa(pid)+"\nstarted=2020-01-01T00:00:00Z\n", 0)
}

// TestGCOrphanGatePRMergeWorktrees_OnlyDeadHolders pins the sweep half of the
// merge-gate leak: a `gate-prmerge-*` worktree GatePRMerge left registered is
// reclaimable ONLY when its holder record names a pid no longer running. No
// record at all (predates the record, or a directory this category simply
// does not recognize) or a live pid both mean "leave it" — the same
// unproven-means-protected rule gcStaleGateDirs already applies to a gate dir
// with no origin.txt.
func TestGCOrphanGatePRMergeWorktrees_OnlyDeadHolders(t *testing.T) {
	repo := makeCargoRepo(t)
	wtParent := filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))

	dead := filepath.Join(wtParent, "gate-prmerge-1111")
	gitDo(t, repo, "worktree", "add", "--detach", dead, "HEAD")
	writeHolder(t, dead, deadPidForTest(t))

	live := filepath.Join(wtParent, "gate-prmerge-2222")
	gitDo(t, repo, "worktree", "add", "--detach", live, "HEAD")
	writeHolder(t, live, os.Getpid())

	unrecorded := filepath.Join(wtParent, "gate-prmerge-3333")
	gitDo(t, repo, "worktree", "add", "--detach", unrecorded, "HEAD")

	got := gcOrphanGatePRMergeWorktrees(repo)
	if len(got) != 1 {
		t.Fatalf("want exactly the one dead-holder checkout, got %d: %+v", len(got), got)
	}
	if got[0].Path != dead {
		t.Fatalf("candidate = %q, want the dead-holder checkout %q", got[0].Path, dead)
	}
}

// TestGCOrphanGatePRMergeWorktrees_NeverAWorktreeWithoutThePrefix pins the
// name match: a registered worktree that is not one of GatePRMerge's own
// throwaway checkouts (an operator's real lane) is never a candidate, however
// stale, and even carrying a file of the same name would be a coincidence
// this category must not act on.
func TestGCOrphanGatePRMergeWorktrees_NeverAWorktreeWithoutThePrefix(t *testing.T) {
	repo := makeCargoRepo(t)
	wtParent := filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))

	lane := filepath.Join(wtParent, "lane-real-work")
	gitDo(t, repo, "worktree", "add", "-b", "lane/real-work", lane)
	writeHolder(t, lane, deadPidForTest(t))

	if got := gcOrphanGatePRMergeWorktrees(repo); len(got) != 0 {
		t.Fatalf("a worktree outside the gate-prmerge-* shape must never be a candidate, got: %+v", got)
	}
}

// TestApplyGC_RemovesAGatePRMergeCandidateAsARegisteredWorktree pins the
// removal path: a GCKindGatePRMerge candidate is a git-registered worktree,
// not a bare directory, so applying it must deregister it too — a plain
// os.RemoveAll leaves `git worktree list` still reporting it (as a missing,
// prunable entry) even though the directory is gone.
func TestApplyGC_RemovesAGatePRMergeCandidateAsARegisteredWorktree(t *testing.T) {
	repo := makeCargoRepo(t)
	wtParent := filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))
	dead := filepath.Join(wtParent, "gate-prmerge-9999")
	gitDo(t, repo, "worktree", "add", "--detach", dead, "HEAD")
	writeHolder(t, dead, deadPidForTest(t))

	cands := gcOrphanGatePRMergeWorktrees(repo)
	if len(cands) != 1 {
		t.Fatalf("setup: want one candidate, got %d", len(cands))
	}

	freed, refused := ApplyGC(cands)
	if len(refused) != 0 {
		t.Fatalf("ApplyGC refused a gate-prmerge candidate: %v", refused)
	}
	if freed <= 0 {
		t.Errorf("freed = %d, want the checkout's bytes counted", freed)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("the checkout directory should be gone, stat err = %v", err)
	}
	remaining := gitWorktreePaths(repo)
	for _, p := range remaining {
		if p == dead {
			t.Fatalf("git still lists %s as a registered worktree after ApplyGC removed it", dead)
		}
	}
}

// #947: a gc sweep unlinks every link in a candidate before deleting it, so it
// never deletes through a linked node_modules into its target. The symlink
// case is the Unix shape; the junction case runs the Windows branch through
// the depinstall seam, a real directory standing in for a junction that
// cannot be unlinked, so a sweep that deletes it anyway loses its file.
func TestApplyGC_NeverDeletesThroughALinkedNodeModules(t *testing.T) {
	const pkg = "node_modules/fakepkg/index.js"
	gatePRMerge := func(t *testing.T) GCCandidate {
		repo := makeCargoRepo(t)
		dead := filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo), "gate-prmerge-9999")
		gitDo(t, repo, "worktree", "add", "--detach", dead, "HEAD")
		writeHolder(t, dead, deadPidForTest(t))
		return GCCandidate{Path: dead, Kind: GCKindGatePRMerge}
	}
	orphan := func(t *testing.T) GCCandidate {
		return GCCandidate{Path: filepath.Join(t.TempDir(), "orphan"), Kind: GCKindOrphanWorktree}
	}
	for _, c := range []struct {
		name string
		cand func(*testing.T) GCCandidate
	}{{"gate-prmerge checkout", gatePRMerge}, {"bare directory", orphan}} {
		t.Run(c.name+"/symlink", func(t *testing.T) {
			cand := c.cand(t)
			target := t.TempDir()
			mkFile(t, filepath.Join(target, pkg), "module.exports = 1\n", 0)
			if err := os.MkdirAll(cand.Path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(target, "node_modules"), filepath.Join(cand.Path, "node_modules")); err != nil {
				t.Fatal(err)
			}
			if _, refused := ApplyGC([]GCCandidate{cand}); len(refused) != 0 {
				t.Fatalf("refused = %v, want the candidate removed", refused)
			}
			if _, err := os.Lstat(cand.Path); !os.IsNotExist(err) {
				t.Errorf("the candidate must be gone, lstat err = %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, pkg)); err != nil {
				t.Errorf("the link's target must keep its contents: %v", err)
			}
		})
		t.Run(c.name+"/junction", func(t *testing.T) {
			cand := c.cand(t)
			mkFile(t, filepath.Join(cand.Path, pkg), "module.exports = 1\n", 0)
			t.Cleanup(depinstall.TreatAsJunction("node_modules"))
			if _, refused := ApplyGC([]GCCandidate{cand}); len(refused) != 1 {
				t.Errorf("refused = %v, want the candidate refused", refused)
			}
			if _, err := os.Stat(filepath.Join(cand.Path, pkg)); err != nil {
				t.Errorf("the junction's target must keep its contents: %v", err)
			}
		})
	}
}

// A warm gate checkout (the stable per-repo one the merge gate reuses so
// path-keyed caches stay warm) has a dead holder between merges by design, so the
// holder alone does not make it garbage: gc proposes it only once it has sat
// idle past the gc age, and never while a live process holds it.
func TestGCOrphanGatePRMergeWorktrees_WarmCheckoutIsReapedOnlyWhenIdle(t *testing.T) {
	repo := makeCargoRepo(t)
	wtParent := filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))
	now := time.Now()

	mk := func(name string, pid int, idle time.Duration) string {
		p := filepath.Join(wtParent, name)
		gitDo(t, repo, "worktree", "add", "--detach", p, "HEAD")
		writeHolder(t, p, pid)
		when := now.Add(-idle)
		if err := os.Chtimes(filepath.Join(p, gatePRMergeHolderFile), when, when); err != nil {
			t.Fatal(err)
		}
		return p
	}
	recent := mk("gate-prmerge-warm", deadPidForTest(t), time.Hour)
	idle := mk("gate-prmerge-localci", deadPidForTest(t), DefaultGCAge+time.Hour)

	got := gcOrphanGatePRMergeWorktreesAt(repo, now)
	if len(got) != 1 || got[0].Path != idle {
		t.Fatalf("want only the idle warm checkout %s, got %+v (the recent one %s must stay)", idle, got, recent)
	}

	// the same idle checkout, held by a live process, is never proposed
	writeHolder(t, idle, os.Getpid())
	old := now.Add(-DefaultGCAge - time.Hour)
	if err := os.Chtimes(filepath.Join(idle, gatePRMergeHolderFile), old, old); err != nil {
		t.Fatal(err)
	}
	if got := gcOrphanGatePRMergeWorktreesAt(repo, now); len(got) != 0 {
		t.Fatalf("a live holder's warm checkout was proposed: %+v", got)
	}
}
