package tdd

import (
	"os"
	"path/filepath"
	"testing"
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

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
