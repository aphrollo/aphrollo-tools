package merge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pruneRepo builds a main repo on `main` plus two linked worktrees: one on a
// branch that has genuinely landed on main (merged with a real merge commit,
// its own tip now an ANCESTOR of main's, never equal to it), and one on a
// brand-new branch created at main's CURRENT tip with no commits of its own
// — the shape a builder starts a lane from before touching anything.
func pruneRepo(t *testing.T) (mainRepo, mergedWT, freshWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	gitDo(t, mainRepo, "branch", "lane/merged")
	mergedWT = filepath.Join(t.TempDir(), "merged")
	gitDo(t, mainRepo, "worktree", "add", "-q", mergedWT, "lane/merged")
	write(t, mergedWT, "landed.go", "package main\n\n// landed\n")
	gitDo(t, mergedWT, "add", "-A")
	gitDo(t, mergedWT, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/merged", "lane/merged")

	freshWT = filepath.Join(t.TempDir(), "fresh")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh", freshWT)

	return mainRepo, mergedWT, freshWT
}

// pruneRepoOnBranch is pruneRepo generalized to an arbitrary trunk name, with
// init.defaultBranch set in the repo's own config so trunk resolution has
// something to find in a repo with no origin remote.
func pruneRepoOnBranch(t *testing.T, trunk string) (mainRepo, mergedWT, freshWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", trunk)
	gitDo(t, mainRepo, "config", "init.defaultBranch", trunk)
	commitInitial(t, mainRepo)

	gitDo(t, mainRepo, "branch", "lane/merged")
	mergedWT = filepath.Join(t.TempDir(), "merged")
	gitDo(t, mainRepo, "worktree", "add", "-q", mergedWT, "lane/merged")
	write(t, mergedWT, "landed.go", "package main\n\n// landed\n")
	gitDo(t, mergedWT, "add", "-A")
	gitDo(t, mergedWT, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/merged", "lane/merged")

	freshWT = filepath.Join(t.TempDir(), "fresh")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh", freshWT)

	return mainRepo, mergedWT, freshWT
}

// A repo whose default branch is NOT "main" (issue #291: the sweep hardcoded
// `rev-parse main` / `--merged main`) must sweep exactly as well as a
// main-default repo — the resolved trunk, not the literal string "main", is
// what decides what counts as landed.
func TestPruneMergedLanesAfterMerge_ResolvesANonMainTrunk(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepoOnBranch(t, "trunk")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be pruned on a trunk-default repo too, got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh's worktree at %s must survive, got err=%v", freshWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
}

// A fresh, unmerged lane sitting at main's own tip survives the post-merge
// sweep; a lane whose branch has actually landed is removed. `git branch
// --merged main` alone would prune BOTH — a branch created minutes earlier
// with no commits of its own trivially satisfies "merged" too, since its tip
// IS main's tip — which is the incident issue #144 records: a fresh lane
// pruned out from under a builder still working in it.
func TestPruneMergedLanesAfterMerge_PrunesLandedWorkSparesAFreshLane(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepo(t)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be pruned (its branch landed on main), got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh's worktree at %s must survive (no work of its own yet), got err=%v", freshWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
	if !strings.Contains(out.String(), "lane/merged") {
		t.Fatalf("stdout = %q, want it to name the pruned lane", out.String())
	}
	if errb.String() != "" {
		t.Fatalf("stderr = %q, want a clean sweep", errb.String())
	}
}

// The worktree running the merge itself must never be swept, whatever its
// own branch's merge state — pruning the ground a process is standing on
// would corrupt the very command that just ran.
func TestPruneMergedLanesAfterMerge_NeverPrunesTheExcludedWorktree(t *testing.T) {
	mainRepo, mergedWT, _ := pruneRepo(t)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, mergedWT, &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("the excluded (current) worktree at %s must survive, got err=%v", mergedWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing pruned — the only merged lane is the excluded one", pruned)
	}
}

// The main clone is skipped by PATH, never by assuming it holds a branch
// named "main": parked on a branch that has genuinely landed (a real
// ancestor of main, tip distinct from main's own), it must still never be
// swept — `git worktree remove` refuses the working tree a process runs in,
// so the wrong outcome here is a noisy, avoidable error, not corruption, but
// the sweep must not even attempt it.
func TestPruneMergedLanesAfterMerge_NeverPrunesTheMainCloneItself(t *testing.T) {
	mainRepo, _, _ := pruneRepo(t)
	gitDo(t, mainRepo, "checkout", "-q", "-b", "wip")
	write(t, mainRepo, "wip.go", "package main\n\n// wip\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "wip work")
	gitDo(t, mainRepo, "checkout", "-q", "main")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge wip", "wip")
	gitDo(t, mainRepo, "checkout", "-q", "wip")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if errb.String() != "" {
		t.Fatalf("stderr = %q, want no attempt (let alone a failed one) to prune the main clone", errb.String())
	}
	for _, p := range pruned {
		if p.Branch == "wip" {
			t.Fatalf("pruned the main clone's own branch: %+v", pruned)
		}
	}
}

// A worktree `git worktree remove` genuinely refuses for a reason OTHER than
// an uncommitted tree — locked, here — is reported on stderr and left alone.
// Pressing on to `branch -D` anyway would delete the branch out from under a
// worktree that is STILL THERE; the removal error must stop it there. The
// failure on one lane must not stop the sweep from reaching the others.
//
// Before issue #382's clean-tree guard this scenario used a DIRTY worktree to
// make git itself refuse the removal; that path is now caught earlier, by
// worktreeHasUncommittedWork, with its own "kept ... uncommitted work"
// message (see TestPruneMergedLanes_KeepsADirtyMergeCommitLandedLane). A lock
// is what still forces `worktree remove` itself to fail on an otherwise CLEAN
// tree, exercising the removal-error path this test is actually for.
func TestPruneMergedLanesAfterMerge_RemovalFailureIsReportedAndTheBranchSurvives(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepo(t)
	gitDo(t, mainRepo, "worktree", "lock", mergedWT)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("a worktree git refused to remove must still be on disk, got err=%v", err)
	}
	if !strings.Contains(errb.String(), "lane/merged") {
		t.Fatalf("stderr = %q, want the failed removal reported by lane", errb.String())
	}
	// The propagated error must be `worktree remove`'s own (git's fatal exit
	// 128 on a locked worktree), never `branch -D`'s (exit 1) from having
	// pressed on to it anyway — that second command failing too, because the
	// worktree is still there holding the branch, is a coincidence of THIS
	// fixture, not a check the code makes; a bare "return err" reporting the
	// wrong command's failure means it read the remove's own error wrong.
	if !strings.Contains(errb.String(), "exit status 128") {
		t.Fatalf("stderr = %q, want it naming the removal's own exit 128, not a later command's", errb.String())
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — the removal failed", pruned)
	}
	// gitValue fatals the test if the branch no longer resolves — a failed
	// worktree removal must never still delete the branch by pressing on to
	// `branch -D` anyway.
	gitValue(t, mainRepo, "rev-parse", "--verify", "refs/heads/lane/merged")
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("the failure on lane/merged must not stop the sweep from leaving lane/fresh alone, got err=%v", err)
	}
}

// A lane created at trunk's tip, with no commits of its own, must survive
// the sweep even after trunk advances past that shared starting point by an
// unrelated commit — issue #382: `git for-each-ref --merged` still lists
// such a lane (its tip is an ancestor of the NEW trunk tip too), and the old
// #144 guard only ever compared a branch's tip against trunk's CURRENT tip,
// so it stopped catching this case the moment trunk moved on. Both a dirty
// and a clean fresh lane must survive: uncommitted builder work must never
// be the thing that decides whether a lane with no commits of its own gets
// read as "merged".
func TestPruneMergedLanes_KeepsAFreshLaneAfterTrunkAdvances(t *testing.T) {
	mainRepo := t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	dirtyWT := filepath.Join(t.TempDir(), "fresh-dirty")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh-dirty", dirtyWT)
	write(t, dirtyWT, "wip.txt", "not committed\n")

	cleanWT := filepath.Join(t.TempDir(), "fresh-clean")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh-clean", cleanWT)

	// Advance trunk past both lanes' shared starting tip.
	write(t, mainRepo, "advance.go", "package main\n\n// advance\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "trunk advances")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(dirtyWT); err != nil {
		t.Fatalf("dirty fresh lane at %s must survive, got err=%v", dirtyWT, err)
	}
	if _, err := os.Stat(cleanWT); err != nil {
		t.Fatalf("clean fresh lane at %s must survive, got err=%v", cleanWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — neither lane has a commit of its own", pruned)
	}
}

// A lane genuinely landed by a real merge commit must still survive the
// sweep while its worktree carries uncommitted work — issue #382: pruning it
// anyway is exactly how 15 minutes of builder work was destroyed. The sweep
// must now check the tree itself, before ever calling `git worktree remove`.
func TestPruneMergedLanes_KeepsADirtyMergeCommitLandedLane(t *testing.T) {
	mainRepo, mergedWT, _ := pruneRepo(t)
	write(t, mergedWT, "dirty.txt", "uncommitted\n")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("dirty merged lane at %s must survive, got err=%v", mergedWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — the lane is dirty", pruned)
	}
	if !strings.Contains(errb.String(), "kept") || !strings.Contains(errb.String(), "uncommitted work") {
		t.Fatalf("stderr = %q, want a kept/uncommitted-work line", errb.String())
	}
}

// The clean-tree guard must not block the case it is not meant to guard: a
// lane landed by a real merge commit, with a clean tree, is pruned exactly
// as it was before issue #382.
func TestPruneMergedLanes_StillPrunesACleanMergeCommitLandedLane(t *testing.T) {
	mainRepo, mergedWT, _ := pruneRepo(t)
	trunk := TrunkBranch(mainRepo)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("clean merged lane at %s must be pruned, got err=%v", mergedWT, err)
	}
	wantSuffix := fmt.Sprintf("(lane/merged, merged into %s)", trunk)
	if !strings.HasPrefix(out.String(), "prune-lanes: pruned ") || !strings.Contains(out.String(), wantSuffix) {
		t.Fatalf("stdout = %q, want a %q line naming lane/merged and %s", out.String(), "prune-lanes: pruned ", wantSuffix)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
	if errb.String() != "" {
		t.Fatalf("stderr = %q, want a clean sweep", errb.String())
	}
}

// pruneRepoOffMainlineBase builds the shape the first-parent guard cannot
// see (issue #644). Trunk gets a real merge commit, so the merged branch's
// own tip is a SECOND parent: an ancestor of trunk — `--merged` lists it —
// that is not on trunk's first-parent chain. A brand-new lane is then
// created AT that commit, the way a builder branches from whatever the
// working checkout happened to be sitting on.
//
// The result is two lanes that trunkFirstParentTips scores identically
// (neither tip is on the mainline) and that could not be more different:
// lane/landed carried a commit of its own which has landed — the sweep's
// whole purpose — while lane/off-mainline has never held a commit at all
// and has a builder about to make its first edit in it.
func pruneRepoOffMainlineBase(t *testing.T) (mainRepo, landedWT, freshWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	gitDo(t, mainRepo, "branch", "lane/landed")
	landedWT = filepath.Join(t.TempDir(), "landed")
	gitDo(t, mainRepo, "worktree", "add", "-q", landedWT, "lane/landed")
	write(t, landedWT, "landed.go", "package main\n\n// landed\n")
	gitDo(t, landedWT, "add", "-A")
	gitDo(t, landedWT, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/landed", "lane/landed")

	// The off-mainline base: lane/landed's tip, now reachable from trunk
	// only as the second parent of the merge commit.
	offBase := gitValue(t, mainRepo, "rev-parse", "refs/heads/lane/landed")
	freshWT = filepath.Join(t.TempDir(), "off-mainline")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/off-mainline", freshWT, offBase)

	return mainRepo, landedWT, freshWT
}

// Issue #644: a lane with NO commits of its own must survive the sweep
// whatever its creation base's position in trunk's topology. The
// first-parent guard (#144/#382) is only a topology PROXY for that
// question, and the proxy fails the moment a lane is created from a commit
// that trunk reaches as a second parent: the tip is "merged", it is not on
// the mainline, and a clean worktree a builder is about to type in gets
// deleted under them.
//
// The same sweep must still remove lane/landed — the two lanes sit on the
// SAME commit, so nothing about the base can tell them apart; only "did
// this branch ever carry a commit of its own" can.
func TestPruneMergedLanes_KeepsAZeroCommitLaneWhoseBaseIsOffTheFirstParentChain(t *testing.T) {
	mainRepo, landedWT, freshWT := pruneRepoOffMainlineBase(t)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("zero-commit lane at %s must survive — its base being off trunk's first-parent chain says nothing about whether it holds work, got err=%v", freshWT, err)
	}
	gitValue(t, mainRepo, "rev-parse", "--verify", "refs/heads/lane/off-mainline")
	if _, err := os.Stat(landedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/landed carried a commit that landed on main and must still be pruned, got err=%v", err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/landed" {
		t.Fatalf("pruned = %+v, want exactly lane/landed", pruned)
	}
	if !strings.Contains(errb.String(), "kept") || !strings.Contains(errb.String(), "lane/off-mainline") {
		t.Fatalf("stderr = %q, want a kept line naming lane/off-mainline", errb.String())
	}
	if !strings.Contains(errb.String(), "no commits of its own") {
		t.Fatalf("stderr = %q, want the keep reason to be the branch holding no commits of its own", errb.String())
	}
}

// Fail CLOSED on doubt. A branch whose reflog cannot be read cannot be
// proven to have carried work of its own, so it is KEPT and the reason is
// said out loud — even here, where the branch genuinely did land a commit
// and would otherwise be the sweep's rightful catch. Deleting a worktree a
// builder is standing in costs them their directory mid-command; keeping a
// stale one costs this line.
func TestPruneMergedLanes_KeepsALaneWhoseReflogCannotBeRead(t *testing.T) {
	mainRepo, landedWT, _ := pruneRepoOffMainlineBase(t)
	if err := os.Remove(filepath.Join(mainRepo, ".git", "logs", "refs", "heads", "lane", "landed")); err != nil {
		t.Fatalf("removing the branch reflog the test is built on: %v", err)
	}

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(landedWT); err != nil {
		t.Fatalf("a lane whose reflog cannot be read must be kept, got err=%v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — no branch here can be proven to hold work of its own", pruned)
	}
	if !strings.Contains(errb.String(), "kept") || !strings.Contains(errb.String(), "lane/landed") {
		t.Fatalf("stderr = %q, want a kept line naming lane/landed", errb.String())
	}
	if !strings.Contains(errb.String(), "reflog") {
		t.Fatalf("stderr = %q, want the keep reason to name the unreadable reflog", errb.String())
	}
}

// Issue #710: a repo whose workflow merges lanes into a LOCAL main and does
// not push after every merge leaves refs/remotes/origin/HEAD trailing local
// main by however much history was never pushed. The question the sweep
// actually asks is "has this lane landed on the branch the merge just
// landed on" — the LOCAL trunk mainRepo (the primary, merge-only checkout)
// is already sitting on — never the remote-tracking ref, which a repo's own
// push cadence controls, not this sweep's correctness.
func TestPruneMergedLanesAfterMerge_ProposesALaneMergedIntoLocalMainEvenWhenOriginTrailsFarBehind(t *testing.T) {
	mainRepo := t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	// A remote-tracking ref frozen at the point the repo was last pushed —
	// origin/HEAD names it, exactly as a real `git clone` would set up.
	gitDo(t, mainRepo, "update-ref", "refs/remotes/origin/main", "main")
	gitDo(t, mainRepo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	// Local main advances with unpushed history — the 837-commit gap the
	// report measured — while origin/main never moves.
	write(t, mainRepo, "unpushed.go", "package main\n\n// unpushed local work\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "local trunk advances, never pushed")

	gitDo(t, mainRepo, "branch", "lane/merged")
	mergedWT := filepath.Join(t.TempDir(), "merged")
	gitDo(t, mainRepo, "worktree", "add", "-q", mergedWT, "lane/merged")
	write(t, mergedWT, "landed.go", "package main\n\n// landed\n")
	gitDo(t, mergedWT, "add", "-A")
	gitDo(t, mergedWT, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/merged", "lane/merged")

	if TrunkBranch(mainRepo) != "origin/main" {
		t.Fatalf("fixture broken: trunkBranch(mainRepo) = %q, want origin/main (the resolution the sweep must NOT use)", TrunkBranch(mainRepo))
	}

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be pruned — it landed on LOCAL main, got err=%v", mergedWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
}

// Issue #710: a sweep that examines lanes and prunes none of them must say
// so — the silent exit is what made the bug invisible for the whole time it
// existed. One line, naming the local trunk it judged against and the count
// of lanes it examined; never a line per lane.
func TestPruneMergedLanesAfterMerge_ReportsExaminedCountAndTrunkWhenNothingIsPruned(t *testing.T) {
	mainRepo := t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	freshWT := filepath.Join(t.TempDir(), "fresh")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh", freshWT)
	write(t, freshWT, "wip.go", "package main\n\n// wip, not merged\n")
	gitDo(t, freshWT, "add", "-A")
	gitDo(t, freshWT, "commit", "-qm", "lane work not yet merged")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — lane/fresh never merged", pruned)
	}
	want := "prune-lanes: examined 1 lane(s) against main; pruned none\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want exactly %q", out.String(), want)
	}
}

// The sweep after a merge must touch only the admin entry of the lane it
// removed. A process under systemd PrivateTmp sees a live worktree in the
// host's /tmp as missing; moving lane/fresh's directory away from the path git
// recorded stands in for that view, and its registration must survive the
// removal of lane/merged.
func TestPruneMergedLanesAfterMerge_LeavesAHiddenWorktreeRegistered(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepo(t)
	if err := os.Rename(freshWT, freshWT+"-elsewhere"); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if len(pruned) != 1 || pruned[0].Worktree != mergedWT {
		t.Fatalf("pruned = %+v, want exactly lane/merged at %s", pruned, mergedWT)
	}
	registered := false
	for _, wt := range mergePruneWorktrees(mainRepo) {
		if cleanWorktreePath(wt.path) == cleanWorktreePath(freshWT) {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("admin entry of the hidden lane/fresh worktree %s was deleted; git lists:\n%s",
			freshWT, gitOut(mainRepo, "worktree", "list", "--porcelain"))
	}
}
