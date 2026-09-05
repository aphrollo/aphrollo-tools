package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
