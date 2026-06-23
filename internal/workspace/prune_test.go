package workspace

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubPRState swaps the gh PR-state seam for a test.
func stubPRState(t *testing.T, fn func(wt, branch string) (string, error)) {
	t.Helper()
	ov := ghPRState
	ghPRState = fn
	t.Cleanup(func() { ghPRState = ov })
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

func TestPrunePlan_NotARepo(t *testing.T) {
	if _, err := PrunePlan(t.TempDir()); err == nil {
		t.Fatal("expected an error for a non-git dir")
	}
}
