package tdd

import (
	"strings"
	"testing"
)

// The third enforcement point for the merge-only rule reports the state
// rather than refusing an action: a primary checkout already parked on a lane
// branch is invisible until a merge lands on the wrong base.

func TestDoctor_SeesAPrimaryCheckoutParkedOffMain(t *testing.T) {
	in := healthyInstall(t)
	primary, _ := primaryRepo(t)
	in.Repo = primary
	gitDo(t, primary, "checkout", "-q", "-b", "lane/parked")

	c := check(t, Doctor(in), "primary checkout on main")
	if c.OK {
		t.Fatal("a primary checkout on a lane branch must fail the check")
	}
	if !strings.Contains(c.Detail, "lane/parked") {
		t.Fatalf("the failure must name the branch it is on, got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "git checkout main") {
		t.Fatalf("the failure must name its fix, got %q", c.Detail)
	}
}

func TestDoctor_AcceptsAPrimaryCheckoutOnMain(t *testing.T) {
	in := healthyInstall(t)
	primary, _ := primaryRepo(t)
	in.Repo = primary

	if c := check(t, Doctor(in), "primary checkout on main"); !c.OK {
		t.Fatalf("a primary checkout on main is the healthy state: %s", c.Detail)
	}
}

func TestDoctor_SkipsThePrimaryCheckoutCheckInALinkedWorktree(t *testing.T) {
	in := healthyInstall(t)
	_, linked := primaryRepo(t)
	in.Repo = linked

	for _, c := range Doctor(in) {
		if c.Name == "primary checkout on main" {
			t.Fatal("the check judges the primary checkout, not the worktree the run happens in")
		}
	}
}

func TestDoctor_SkipsThePrimaryCheckoutCheckInARepoWithNoWorktrees(t *testing.T) {
	in := healthyInstall(t)
	repo := t.TempDir()
	gitInit(t, repo)
	gitDo(t, repo, "checkout", "-q", "-B", "feature")
	commitInitial(t, repo)
	in.Repo = repo

	for _, c := range Doctor(in) {
		if c.Name == "primary checkout on main" {
			t.Fatal("an ordinary single-checkout clone has no primary/lane split to keep")
		}
	}
}
