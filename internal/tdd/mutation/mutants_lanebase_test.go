package mutation

import (
	"strings"
	"testing"
)

// laneBaseRef prefers `origin/main`, and nothing in this package ever fetches,
// so that remote-tracking ref is whatever it last happened to be. A lane that
// catches up by merging its LOCAL main then measures against a base from
// BEFORE that merge, and the run charges the lane for every change main made
// in between — a real run measured 40 files and two crates the lane never
// opened.
//
// The base a lane's own diff is taken against must be the newest trunk commit
// already reachable from HEAD: anything already merged in is by definition not
// what the lane is proposing.
func TestLaneBaseSHA_ExcludesTrunkWorkTheLaneAlreadyMergedIn(t *testing.T) {
	repo := makeGoRepo(t)
	gitDo(t, repo, "branch", "-M", "main")
	// A stale remote-tracking ref, exactly as a checkout that has not fetched
	// carries: origin/main pinned at the point the lane branched.
	branchedAt := gitValue(t, repo, "rev-parse", "HEAD")
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", branchedAt)

	// main moves on with a file the lane never touches...
	write(t, repo, "trunk_only.go", "package m\n\nfunc TrunkOnly() int { return 1 }\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "trunk adds a file the lane never opens")
	trunkTip := gitValue(t, repo, "rev-parse", "HEAD")

	// ...the lane branches from the older point, does its own work, then
	// catches up by merging local main — the remedy the push guard prints.
	gitDo(t, repo, "checkout", "-q", "-b", "lane/work", branchedAt)
	write(t, repo, "lane_only.go", "package m\n\nfunc LaneOnly() int { return 2 }\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "the lane's own change")
	gitDo(t, repo, "merge", "-q", "--no-edit", "main")

	base := laneBaseSHA(repo)
	if base == "" {
		t.Fatal("no base resolved")
	}
	if base == branchedAt {
		t.Fatalf("base is the pre-merge point %s — the run would measure trunk's own changes as the lane's", short(base))
	}
	if base != trunkTip {
		t.Errorf("base = %s, want trunk's tip %s: the newest trunk commit already merged into the lane", short(base), short(trunkTip))
	}

	// The consequence the base exists to control: what the run measures.
	diff := gitValue(t, repo, "diff", "--name-only", base, "HEAD")
	if strings.Contains(diff, "trunk_only.go") {
		t.Errorf("measured files %q include trunk's own file — the lane is charged for work it merged, not wrote", diff)
	}
	if !strings.Contains(diff, "lane_only.go") {
		t.Errorf("measured files %q omit the lane's own change", diff)
	}
}

// A lane that has NOT caught up still measures against where it branched, so
// the ordinary case is unchanged.
func TestLaneBaseSHA_UnmergedLaneStillMeasuresFromWhereItBranched(t *testing.T) {
	repo := makeGoRepo(t)
	gitDo(t, repo, "branch", "-M", "main")
	branchedAt := gitValue(t, repo, "rev-parse", "HEAD")
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", branchedAt)

	write(t, repo, "trunk_only.go", "package m\n\nfunc TrunkOnly() int { return 1 }\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "trunk moves on")

	gitDo(t, repo, "checkout", "-q", "-b", "lane/behind", branchedAt)
	write(t, repo, "lane_only.go", "package m\n\nfunc LaneOnly() int { return 2 }\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "the lane's own change")

	if base := laneBaseSHA(repo); base != branchedAt {
		t.Errorf("base = %s, want the branch point %s — an un-caught-up lane has nothing of trunk's to exclude", short(base), short(branchedAt))
	}
}
