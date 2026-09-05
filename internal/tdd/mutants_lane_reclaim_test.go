package tdd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// One mutants tree per lane costs one warm target dir per lane, and the run
// refuses to start below 15 GB free per job. A lane worktree that is removed
// when its work merges must therefore take its mutants tree with it, or the
// mutants root grows without bound until the refusal
//
//	gate: mutation run refused — 12 GB free on the build drive, 30 GB needed
//
// stops every lane on the box.
//
// The lane directory is named by a hash of its checkout path, which cannot be
// read back, so preparing a tree records the path it belongs to beside it.
func TestReclaimStaleMutantsLanes_RemovesTheTreeOfALaneThatIsGone(t *testing.T) {
	root := makeGoRepo(t)
	gone := addWorktree(t, root, "departed-lane")

	tree := MutantsWorktreeDir(gone)
	mustMkdir(t, tree)
	writeMutantsLaneMarker(tree, gone)

	// The lane merges and its worktree is removed, which is what leaves the
	// tree behind with nobody to claim it.
	gitDo(t, root, "worktree", "remove", "--force", gone)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Errorf("%q survived its lane's removal (stat err = %v) — every merged lane leaves a warm target dir behind and the drive fills", tree, err)
	}
}

// ...and a lane that still exists keeps its tree: reclaiming a live lane's
// warm build is the cold rebuild this whole design removes, and doing it under
// a running measurement is the collision the per-lane split just fixed.
func TestReclaimStaleMutantsLanes_KeepsTheTreeOfALaneThatIsStillThere(t *testing.T) {
	root := makeGoRepo(t)
	live := addWorktree(t, root, "live-lane")

	tree := MutantsWorktreeDir(live)
	mustMkdir(t, tree)
	writeMutantsLaneMarker(tree, live)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(tree); err != nil {
		t.Errorf("%q was removed while its lane still exists: %v", tree, err)
	}
}

// A directory with no marker is not evidence of anything — it predates the
// marker, or something else made it — so the sweep leaves it alone rather than
// deleting a tree it cannot account for.
func TestReclaimStaleMutantsLanes_LeavesADirectoryItCannotAccountFor(t *testing.T) {
	root := makeGoRepo(t)
	unmarked := filepath.Join(MutantsRootDir(root), "0123456789abcdef")
	mustMkdir(t, unmarked)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(unmarked); err != nil {
		t.Errorf("%q was removed although nothing said which lane it belongs to: %v", unmarked, err)
	}
}

// Before the build directory moved to the repo's one shared target dir, every
// lane's tree carried its own — 8-18 GB each, nine of them measured on borld.
// A binary that no longer builds there must still reclaim them, or the drive
// keeps paying for a layout nothing uses; and the LIVE lane's tree stays,
// because its worktree is still the warm checkout the next run resets.
func TestReclaimStaleMutantsLanes_RemovesALegacyPerLaneTargetDir(t *testing.T) {
	root := makeGoRepo(t)
	live := addWorktree(t, root, "live-lane")
	prev := mutantsRunningFn
	t.Cleanup(func() { mutantsRunningFn = prev })
	mutantsRunningFn = func() bool { return false }

	tree := MutantsWorktreeDir(live)
	legacy := filepath.Join(tree, "target")
	mustMkdir(t, filepath.Join(legacy, "debug", "deps"))
	writeMutantsLaneMarker(tree, live)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("%q survived (stat err = %v) — a per-lane target dir is a layout nothing builds into any more", legacy, err)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("%q was removed while its lane still exists: %v", tree, err)
	}
}

// ...but never under a running producer. A run started by the binary that
// still built per lane holds that directory open for hours; deleting it
// mid-link is the collision the lock exists to prevent.
func TestReclaimStaleMutantsLanes_KeepsALegacyTargetDirWhileARunIsAlive(t *testing.T) {
	root := makeGoRepo(t)
	live := addWorktree(t, root, "live-lane")
	prev := mutantsRunningFn
	t.Cleanup(func() { mutantsRunningFn = prev })
	mutantsRunningFn = func() bool { return true }

	tree := MutantsWorktreeDir(live)
	legacy := filepath.Join(tree, "target")
	mustMkdir(t, filepath.Join(legacy, "debug", "deps"))
	writeMutantsLaneMarker(tree, live)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("%q was removed under a live mutation run: %v", legacy, err)
	}
}

// An alternate is born when a commit overlap makes the lane's own base
// unsafe to reuse (chooseMutantsWorktree, mutants_job.go); it carries the
// SAME lane marker as the base, so "the lane still exists" — the rule that
// rightly keeps the base warm — must not also keep an alternate whose job
// is long over: the base is the one tree the lane's next run reuses, and an
// alternate earns no such reuse (issue #404).
func TestReclaimStaleMutantsLanes_RemovesAnAlternateWhoseJobIsOverEvenThoughItsLaneStillExists(t *testing.T) {
	root := makeGoRepo(t)
	live := addWorktree(t, root, "live-lane")

	alt := MutantsWorktreeDir(live) + "-deadbeef"
	mustMkdir(t, alt)
	writeMutantsLaneMarker(alt, live)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(alt); !os.IsNotExist(err) {
		t.Errorf("%q survived (stat err = %v) — no live job holds it, and its lane's base is the tree that stays warm, not this one", alt, err)
	}
}

// ...but not while a live job still names it as its own Worktree: that is
// exactly the reservation issue #405 introduced chooseMutantsWorktree to
// make, and liveness is what must decide here, not the hook that fires when
// a job exits cleanly — the same check also protects an alternate whose job
// was killed rather than exiting on its own.
func TestReclaimStaleMutantsLanes_KeepsAnAlternateAJobStillHolds(t *testing.T) {
	root := makeGoRepo(t)
	live := addWorktree(t, root, "live-lane")

	alt := MutantsWorktreeDir(live) + "-deadbeef"
	mustMkdir(t, alt)
	writeMutantsLaneMarker(alt, live)
	saveMutantsJob(MutantsJob{Repo: commonGitDir(root), Worktree: alt, PID: os.Getpid(), Started: time.Now()})

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(alt); err != nil {
		t.Errorf("%q was removed while a live job still holds it: %v", alt, err)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// The Go job does not run in the lane's worktree: a mutated gate test rewrote
// the real repository through the linked worktree's shared .git, so the run
// now happens in a private CLONE beside it, at `<worktree>-clone`. That is a
// second full tree per lane, and the sweep only knew about the first — so a
// merged lane still left one behind, on a box that already refuses to start a
// run below 15 GB free per job.
func TestReclaimStaleMutantsLanes_AlsoRemovesTheRunCloneBesideTheTree(t *testing.T) {
	root := makeGoRepo(t)
	gone := addWorktree(t, root, "departed-lane")

	tree := MutantsWorktreeDir(gone)
	clone := goMutantsCloneDir(tree)
	mustMkdir(t, tree)
	mustMkdir(t, clone)
	writeMutantsLaneMarker(tree, gone)
	gitDo(t, root, "worktree", "remove", "--force", gone)

	reclaimStaleMutantsLanes(root)

	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("%q survived its lane's removal (stat err = %v) — the clone is a second full tree per lane", clone, err)
	}
}
