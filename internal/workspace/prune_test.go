package workspace

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPrune_DetectsAndRemovesStale(t *testing.T) {
	repo, wt, _ := preparedRepo(t)
	// Delete the worktree dir out from under git, leaving a stale admin record.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}

	// Dry-run reports it without removing the admin record.
	var dry, dryErr bytes.Buffer
	if err := p.Run(false, &dry, &dryErr); err != nil {
		t.Fatalf("dry Run: %v\n%s", err, dryErr.String())
	}
	if !strings.Contains(dry.String(), "would prune") {
		t.Errorf("dry-run should preview a prune:\n%s", dry.String())
	}

	// Apply removes it and reports the count.
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("apply Run: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "pruned 1 stale worktree") {
		t.Errorf("apply should report one pruned worktree:\n%s", out.String())
	}

	// A second prune finds nothing.
	var out2, errb2 bytes.Buffer
	if err := p.Run(true, &out2, &errb2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), "no stale worktrees") {
		t.Errorf("second prune should be a no-op:\n%s", out2.String())
	}
}

func TestPrunePlan_NotARepo(t *testing.T) {
	if _, err := PrunePlan(t.TempDir()); err == nil {
		t.Fatal("expected an error for a non-git dir")
	}
}
