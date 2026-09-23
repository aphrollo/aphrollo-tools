package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Every verb that removes a worktree must touch the admin entry of THAT
// worktree only. An agent session under systemd PrivateTmp sees a live
// worktree in the host's /tmp as missing; a bare `git worktree prune` run
// from there deletes that worktree's registration while its owner is still
// using it. Each test below adds a second worktree, moves its directory out of
// the path git recorded (the private-tmp view), runs the verb on the FIRST
// worktree, and requires the hidden one to still be registered.

// hiddenWorktree adds a linked worktree on branch and moves its directory away
// from the path git recorded, returning that recorded path.
func hiddenWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	plan, err := BuildPlan(Request{Repo: repo, Branch: branch, NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("prepare hidden worktree: %v\n%s", err, errb.String())
	}
	if err := os.Rename(plan.Worktree, plan.Worktree+"-elsewhere"); err != nil {
		t.Fatal(err)
	}
	return plan.Worktree
}

// requireRegistered fails unless git still lists wt as a worktree of repo.
func requireRegistered(t *testing.T, repo, wt string) {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	for _, e := range parseWorktreeList(string(out)) {
		if samePath(e.Path, wt) {
			return
		}
	}
	t.Errorf("admin entry of the hidden worktree %s was deleted; git now lists:\n%s", wt, out)
}

func TestPrune_SweepLeavesAHiddenWorktreeRegistered(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	hidden := hiddenWorktree(t, repo, "feat/hidden")
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	head := headSHA(t, wt)
	stubPRHeadOid(t, func(_, _ string) (string, error) { return head, nil })

	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the merged worktree should be gone, stat err = %v\n%s", err, out.String())
	}
	requireRegistered(t, repo, hidden)
}

func TestPruneTicket_LeavesAHiddenWorktreeRegistered(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	hidden := hiddenWorktree(t, repo, "feat/hidden")

	p, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the ticket's worktree should be gone, stat err = %v", err)
	}
	requireRegistered(t, repo, hidden)
}

func TestPruneTicket_AlreadyGoneLeavesAHiddenWorktreeRegistered(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	hidden := hiddenWorktree(t, repo, "feat/hidden")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	p, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	requireRegistered(t, repo, hidden)
}

func TestRemove_LeavesAHiddenWorktreeRegistered(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	hidden := hiddenWorktree(t, repo, "feat/hidden")

	r, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := r.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the removed worktree should be gone, stat err = %v", err)
	}
	requireRegistered(t, repo, hidden)
}

// TestRemove_AlreadyGoneDropsOnlyItsOwnEntry: a worktree whose directory was
// deleted out of band still has its own admin entry dropped by `remove`, and
// the hidden sibling keeps its entry.
func TestRemove_AlreadyGoneDropsOnlyItsOwnEntry(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	hidden := hiddenWorktree(t, repo, "feat/hidden")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	r, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := r.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	lst, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range parseWorktreeList(string(lst)) {
		if samePath(e.Path, wt) {
			t.Errorf("the removed worktree %s is still registered:\n%s", wt, lst)
		}
	}
	requireRegistered(t, repo, hidden)
}

func TestPrune_StaleSweepLeavesAHiddenWorktreeRegistered(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	stubNow(t, at)
	stubPRState(t, func(_, _ string) (string, error) { return "", nil })
	repo, wt := detachedIdleWorktree(t, at, 5)
	hidden := hiddenWorktree(t, repo, "feat/hidden")

	s, err := StaleSweepPlan(repo, 3*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := s.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the stale worktree should be gone, stat err = %v\n%s", err, out.String())
	}
	requireRegistered(t, repo, hidden)
}
