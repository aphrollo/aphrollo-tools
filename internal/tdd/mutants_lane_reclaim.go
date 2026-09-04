package tdd

import (
	"os"
	"path/filepath"
	"strings"
)

// mutantsLaneMarkerName sits beside a lane's mutants tree rather than inside
// it: the tree is a git worktree the run resets with `--hard`, and anything
// tracked-looking in there is noise the run has to reason about.
const mutantsLaneMarkerName = ".lane"

// writeMutantsLaneMarker records which checkout a mutants tree was created
// for. The directory itself is named by a hash of that path, which cannot be
// read back, so without the marker a sweep has no way to tell a live lane's
// warm tree from a merged one's abandoned 15 GB.
func writeMutantsLaneMarker(tree, lane string) {
	_ = os.WriteFile(tree+mutantsLaneMarkerName, []byte(filepath.Clean(lane)+"\n"), 0o600)
}

// reclaimStaleMutantsLanes removes the mutants tree of every lane whose
// checkout no longer exists. One tree per lane means one warm target dir per
// lane, and the run refuses to start below 15 GB free per job -- so a merged
// lane that left its tree behind eventually stops every other lane on the box
// with `mutation run refused`.
//
// It is deliberately conservative in both directions: a directory with no
// marker is left alone (it predates the marker, or something else made it),
// and a lane that still exists keeps its tree, because reclaiming a live
// lane's warm build is the cold rebuild this design exists to avoid.
func reclaimStaleMutantsLanes(repoRoot string) {
	root := MutantsRootDir(repoRoot)
	for _, e := range readDir(root) {
		if !e.IsDir() {
			continue
		}
		tree := filepath.Join(root, e.Name())
		lane, err := os.ReadFile(tree + mutantsLaneMarkerName)
		if err != nil {
			continue
		}
		path := strings.TrimSpace(string(lane))
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			continue
		}
		// Unregister before deleting: the tree is a linked worktree, and a
		// directory removed behind git's back leaves an entry that makes every
		// later `worktree add` at that path fail.
		_, _ = git(repoRoot, "worktree", "remove", "--force", tree)
		_ = os.RemoveAll(tree)
		// The Go job runs in a private clone beside the tree rather than in
		// the linked worktree itself, so a lane leaves TWO full trees behind
		// and both have to go.
		_ = os.RemoveAll(goMutantsCloneDir(tree))
		_ = os.Remove(tree + mutantsLaneMarkerName)
	}
}
