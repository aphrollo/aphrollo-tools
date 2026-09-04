package workspace

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGh puts a fake `gh` on PATH that prints stdout and exits with exitCode,
// so the REAL ghPRHeadOid closure (not the stubPRHeadOid seam) can be exercised
// without the network or a real gh install. POSIX: a shebang shell script.
// Windows can't run one directly (no shebang dispatch through CreateProcess,
// and Go's os/exec refuses a file with no PATHEXT-recognized extension even
// given a full path) — a .bat with the equivalent lines serves as the fake.
func fakeGh(t *testing.T, stdout string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		body := fmt.Sprintf("@echo off\r\necho %s\r\nexit /b %d\r\n", stdout, exitCode)
		if err := os.WriteFile(filepath.Join(dir, "gh.bat"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		body := fmt.Sprintf("#!/bin/sh\necho \"%s\"\nexit %d\n", stdout, exitCode)
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGhPRHeadOid_ReturnsSHAOnGhSuccess proves the real ghPRHeadOid closure
// (prune.go:144) returns gh's trimmed stdout as the SHA with a nil error when
// gh exits 0 — the CONDITIONALS_NEGATION mutant at its `if err != nil` (line
// 148) flips this to the error branch (formatting the nil err into a bogus
// message) instead of returning the SHA.
func TestGhPRHeadOid_ReturnsSHAOnGhSuccess(t *testing.T) {
	fakeGh(t, "abc123", 0)
	sha, err := ghPRHeadOid(t.TempDir(), "feat/x")
	if err != nil {
		t.Fatalf("ghPRHeadOid: %v", err)
	}
	if sha != "abc123" {
		t.Errorf("sha = %q, want %q", sha, "abc123")
	}
}

// TestGhPRHeadOid_ReturnsErrorOnGhFailure proves the real ghPRHeadOid closure
// propagates a genuine gh failure as a non-nil error rather than treating its
// stdout as a SHA — the CONDITIONALS_NEGATION mutant at line 148 flips this to
// skip the error branch and return gh's failure output as if it were a valid
// SHA.
func TestGhPRHeadOid_ReturnsErrorOnGhFailure(t *testing.T) {
	fakeGh(t, "gh: authentication required", 1)
	sha, err := ghPRHeadOid(t.TempDir(), "feat/x")
	if err == nil {
		t.Fatalf("expected an error, got sha %q", sha)
	}
	if !strings.Contains(err.Error(), "feat/x") || !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("error should name the branch and carry gh's message, got: %v", err)
	}
	if sha != "" {
		t.Errorf("sha on failure = %q, want empty", sha)
	}
}

// stubPRState swaps the gh PR-state seam for a test.
func stubPRState(t *testing.T, fn func(wt, branch string) (string, error)) {
	t.Helper()
	ov := ghPRState
	ghPRState = fn
	t.Cleanup(func() { ghPRState = ov })
}

// stubPRHeadOid swaps the gh PR head-ref-oid seam for a test.
func stubPRHeadOid(t *testing.T, fn func(wt, branch string) (string, error)) {
	t.Helper()
	ov := ghPRHeadOid
	ghPRHeadOid = fn
	t.Cleanup(func() { ghPRHeadOid = ov })
}

// commitFile commits a new file into wt, simulating a builder who reuses a
// merged branch/worktree for follow-up work.
func commitFile(t *testing.T, wt, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(wt, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", wt, "add", name).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wt, "commit", "-qm", "follow-up after merge").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	sha, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(sha))
}

// headSHA returns wt's current HEAD commit, for stubbing ghPRHeadOid to match
// reality in tests that don't exercise the #163 "commits after the merge" gap.
func headSHA(t *testing.T, wt string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// dirty writes an uncommitted file into a worktree so `git status --porcelain`
// is non-empty.
func dirty(t *testing.T, wt string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrune_RemovesMergedCleanWorktree(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	head := headSHA(t, wt)
	stubPRHeadOid(t, func(_, b string) (string, error) {
		if b == branch {
			return head, nil
		}
		return "", nil
	})

	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}

	// Dry-run lists what WOULD be pruned without removing.
	var dry, dryErr bytes.Buffer
	if err := p.Run(false, &dry, &dryErr); err != nil {
		t.Fatalf("dry Run: %v\n%s", err, dryErr.String())
	}
	if !strings.Contains(dry.String(), "would prune") || !strings.Contains(dry.String(), wt) {
		t.Errorf("dry-run should list the merged worktree:\n%s", dry.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("dry-run must not remove the worktree: %v", err)
	}

	// Apply removes it.
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("apply Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("merged clean worktree should be gone, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "pruned: "+wt) || !strings.Contains(out.String(), "merged") {
		t.Errorf("receipt should report the pruned worktree as merged:\n%s", out.String())
	}
}

func TestPrune_SkipsOpenPR(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "OPEN", nil
		}
		return "", nil
	})
	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("open-PR worktree must be kept: %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) || !strings.Contains(out.String(), "open PR") {
		t.Errorf("receipt should skip the open-PR worktree:\n%s", out.String())
	}
}

func TestPrune_SkipsNoPR(t *testing.T) {
	repo, wt, _ := preparedRepo(t)
	stubPRState(t, func(_, _ string) (string, error) { return "", nil }) // no PR
	p, _ := PrunePlan(repo)
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("no-PR worktree must be kept: %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) || !strings.Contains(out.String(), "no PR") {
		t.Errorf("receipt should skip the no-PR worktree:\n%s", out.String())
	}
}

func TestPrune_SkipsClosedPR(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "CLOSED", nil
		}
		return "", nil
	})
	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("closed-PR worktree must be kept (not merged): %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) || !strings.Contains(out.String(), "closed PR") {
		t.Errorf("receipt should skip the closed-PR worktree:\n%s", out.String())
	}
}

func TestPrune_SkipsOnGHError(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	// A genuine gh failure (auth/network/gh-missing) must SKIP, never prune — the
	// state is unknown, so the fail-safe direction is to keep the worktree.
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "", fmt.Errorf("gh: HTTP 401: bad credentials")
		}
		return "", nil
	})
	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("a worktree whose PR state could not be read must be kept: %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) || !strings.Contains(out.String(), "could not check PR state") {
		t.Errorf("receipt should skip on a gh error with a clear reason:\n%s", out.String())
	}
}

// TestPrune_KeepsAMergedWorktreeThatHasCommitsAfterTheMerge is issue #163's
// scenario: a builder reuses a merged branch's worktree for follow-up work and
// commits it (so `git status --porcelain` is clean again). The sweep must not
// treat "PR state MERGED + tree clean" as sufficient — HEAD has moved past what
// the PR actually merged (gh's headRefOid), so the worktree must be kept.
func TestPrune_KeepsAMergedWorktreeThatHasCommitsAfterTheMerge(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	mergedSHA, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	stubPRHeadOid(t, func(_, b string) (string, error) {
		if b == branch {
			return strings.TrimSpace(string(mergedSHA)), nil
		}
		return "", nil
	})

	// Follow-up commit made in the worktree AFTER the PR merged — HEAD now
	// differs from what gh's headRefOid says was actually merged.
	commitFile(t, wt, "followup.txt")

	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree with commits after the merge must be kept: %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) {
		t.Errorf("receipt should skip the worktree:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "local commits beyond the merged PR") {
		t.Errorf("skip reason should say local commits are beyond what was merged:\n%s", out.String())
	}
}

func TestPrune_SkipsDirtyMerged(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	dirty(t, wt)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	p, _ := PrunePlan(repo)
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("dirty merged worktree must be kept without --force: %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) || !strings.Contains(out.String(), "dirty") {
		t.Errorf("receipt should skip the dirty worktree:\n%s", out.String())
	}
}

func TestPrune_ForceRemovesDirtyMerged(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	dirty(t, wt)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	head := headSHA(t, wt)
	stubPRHeadOid(t, func(_, b string) (string, error) {
		if b == branch {
			return head, nil
		}
		return "", nil
	})
	p, _ := PrunePlan(repo)
	p.Force = true
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("--force should remove a dirty merged worktree, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "pruned: "+wt) {
		t.Errorf("receipt should report the forced prune:\n%s", out.String())
	}
}

func TestPrune_SkipsCurrentWorktree(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	t.Chdir(wt) // stand inside the worktree
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("the cwd worktree must never be removed: %v", err)
	}
	if !strings.Contains(out.String(), "skip: "+wt) || !strings.Contains(out.String(), "current") {
		t.Errorf("receipt should skip the current worktree:\n%s", out.String())
	}
}

func TestPrune_TallyAndAdminCleanup(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	head := headSHA(t, wt)
	stubPRHeadOid(t, func(_, b string) (string, error) {
		if b == branch {
			return head, nil
		}
		return "", nil
	})
	// Also leave a stale admin record (a worktree whose dir is gone) to prove the
	// admin-record prune is folded in.
	plan, err := BuildPlan(Request{Repo: repo, Branch: "feat/stale", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var b1, b2 bytes.Buffer
	if err := Apply(plan, &b1, &b2); err != nil {
		t.Fatalf("prepare stale: %v\n%s", err, b2.String())
	}
	if err := os.RemoveAll(plan.Worktree); err != nil {
		t.Fatal(err)
	}

	p, _ := PrunePlan(repo)
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("merged worktree should be gone")
	}
	// Admin record for the stale worktree is gone too.
	lst, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(lst), "feat-stale") {
		t.Errorf("stale admin record should have been pruned:\n%s", lst)
	}
	if !strings.Contains(out.String(), "pruned 1") {
		t.Errorf("receipt should tally one pruned worktree:\n%s", out.String())
	}
}

// TestExcludeMainClone_DropsByPathNotListPosition is issue #172: linkedWorktrees
// used to drop `git worktree list --porcelain`'s FIRST entry to exclude the
// main checkout, trusting an ordering nothing enforces. excludeMainClone must
// drop the main clone by comparing its PATH against repo, so it is excluded
// wherever it lands in the list — proven here with a synthetic list where the
// main clone is NOT first.
func TestExcludeMainClone_DropsByPathNotListPosition(t *testing.T) {
	repo := filepath.Join("C:", "spaces", "aphrollo")
	linked1 := filepath.Join("C:", "spaces", ".worktrees", "aphrollo", "lane-a")
	linked2 := filepath.Join("C:", "spaces", ".worktrees", "aphrollo", "lane-b")
	// The main clone sits LAST, not first — the exact ordering violation
	// mergeprune.go's comment warns entries[1:] cannot defend against.
	entries := []worktreeEntry{
		{Path: linked1, Branch: "lane-a"},
		{Path: linked2, Branch: "lane-b"},
		{Path: repo, Branch: "main"},
	}
	got := excludeMainClone(entries, repo)
	if len(got) != 2 {
		t.Fatalf("excludeMainClone should keep exactly the 2 linked worktrees, got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Path == repo {
			t.Errorf("excludeMainClone must never keep the main clone %q, got %+v", repo, got)
		}
	}
	if got[0].Path != linked1 || got[1].Path != linked2 {
		t.Errorf("excludeMainClone should preserve the linked worktrees' relative order, got %+v", got)
	}
}

func TestPrunePlan_NotARepo(t *testing.T) {
	if _, err := PrunePlan(t.TempDir()); err == nil {
		t.Fatal("expected an error for a non-git dir")
	}
}

// --- per-ticket prune: targeted, idempotent removal of one ticket's worktree ---

// TestPruneTicket_RemovesWorktree is the happy path: a dry-run lists what would
// go without touching anything; apply removes exactly that ticket's worktree.
func TestPruneTicket_RemovesWorktree(t *testing.T) {
	repo, wt, branch := preparedRepo(t)

	p, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatalf("PruneTicketPlan: %v", err)
	}

	var dry, dryErr bytes.Buffer
	if err := p.Run(false, &dry, &dryErr); err != nil {
		t.Fatalf("dry Run: %v\n%s", err, dryErr.String())
	}
	if !strings.Contains(dry.String(), "would prune") || !strings.Contains(dry.String(), wt) {
		t.Errorf("dry-run should list the worktree without removing it:\n%s", dry.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("dry-run must not remove the worktree: %v", err)
	}

	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("apply Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "pruned: "+wt) {
		t.Errorf("receipt should report the pruned worktree:\n%s", out.String())
	}
}

// TestPruneTicket_IdempotentReRun is the core guarantee from the ticket: a second
// prune of the same already-gone worktree is a no-op success ("already gone", no
// error), so a post-merge cleanup path can re-run on redelivery without wedging.
func TestPruneTicket_IdempotentReRun(t *testing.T) {
	repo, wt, branch := preparedRepo(t)

	p1, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatalf("PruneTicketPlan: %v", err)
	}
	var o1, e1 bytes.Buffer
	if err := p1.Run(true, &o1, &e1); err != nil {
		t.Fatalf("first Run: %v\n%s", err, e1.String())
	}

	// Second run: the worktree is already gone. Must succeed, not error.
	p2, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatalf("PruneTicketPlan (2): %v", err)
	}
	var o2, e2 bytes.Buffer
	if err := p2.Run(true, &o2, &e2); err != nil {
		t.Fatalf("idempotent re-run must succeed, got: %v\n%s", err, e2.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should still be gone after re-run, stat err = %v", err)
	}
	if !strings.Contains(o2.String(), "already gone") {
		t.Errorf("re-run should report the worktree as already gone:\n%s", o2.String())
	}
}

// TestPruneTicket_KeepsLocalBranch pins the split from the `remove` verb: a
// per-ticket prune clears the WORKTREE only (matching the sweep's semantics);
// deleting the local branch stays `remove`'s job.
func TestPruneTicket_KeepsLocalBranch(t *testing.T) {
	repo, _, branch := preparedRepo(t)

	p, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatalf("PruneTicketPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if !localBranchListed(t, repo, branch) {
		t.Errorf("per-ticket prune must leave the local branch %s in place", branch)
	}
}

// TestPruneTicket_SweepsStaleAdminRecord covers the case where the worktree dir
// was removed out-of-band: prune reports it gone AND folds in the admin-record
// prune so git no longer lists the stale worktree.
func TestPruneTicket_SweepsStaleAdminRecord(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	p, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatalf("PruneTicketPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "already gone") {
		t.Errorf("receipt should report the already-removed worktree as gone:\n%s", out.String())
	}
	lst, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(lst), "feat-x") {
		t.Errorf("stale admin record should have been pruned:\n%s", lst)
	}
}

// TestPruneTicket_RefusesCwd keeps the standing-in-it guard.
func TestPruneTicket_RefusesCwd(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	t.Chdir(wt)
	if _, err := PruneTicketPlan(repo, branch, ""); err == nil {
		t.Fatal("PruneTicketPlan should refuse to prune the worktree the caller stands in")
	}
}

func TestPruneTicketPlan_NoRepo(t *testing.T) {
	if _, err := PruneTicketPlan("", "feat/x", ""); err == nil {
		t.Fatal("expected an error when the repo arg is empty")
	}
}
