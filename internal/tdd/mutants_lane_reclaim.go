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
// checkout no longer exists, the legacy per-lane target dir of every lane
// that still does, and every ALTERNATE tree (chooseMutantsWorktree,
// mutants_job.go) no live job still holds. The run refuses to start below
// 15 GB free per job -- so a merged lane, or an abandoned alternate, that
// left its tree behind eventually stops every other lane on the box with
// `mutation run refused`.
//
// It is deliberately conservative in every direction it can be: a directory
// with no marker is left alone (it predates the marker, or something else
// made it), a lane's BASE tree survives its lane existing, because a live
// lane's warm checkout is what its next run resets rather than re-adds, and
// an alternate survives while any live job still names it as its Worktree.
//
// The build directory used to live INSIDE each lane's tree; it is now the
// repo's one MutantsTargetDir, shared. A tree's own `target` is therefore a
// layout nothing builds into any more -- 8-18 GB per lane, measured -- and is
// reclaimed whether or not its lane is alive. Never under a live producer: a
// run started by the binary that still built per lane holds that directory
// for hours, and deleting it mid-link is the collision the run lock exists to
// prevent.
func reclaimStaleMutantsLanes(repoRoot string) {
	root := MutantsRootDir(repoRoot)
	producerAlive := mutantsRunningFn()
	liveJobs := RunningMutantsJobs(commonGitDir(repoRoot))
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
			// An alternate carries the SAME lane marker as its lane's base
			// (prepareMutantsWorktree writes j.RepoRoot either way), so "the
			// lane still exists" cannot be what keeps an alternate the way
			// it keeps the base: the base is reused on purpose, and an
			// alternate earns no such reuse — the job that needed one
			// already has its own base to fall back to next time. Once no
			// live job still holds it, an alternate is exactly as
			// reclaimable as if its lane were gone (issue #404); a job
			// killed rather than exited cleanly is caught by the same
			// liveness check, since a lane-gone hook never fires for it.
			if tree != MutantsWorktreeDir(path) && !mutantsWorktreeHeldBy(liveJobs, tree) {
				removeMutantsTree(repoRoot, tree)
				continue
			}
			if !producerAlive {
				_ = os.RemoveAll(filepath.Join(tree, "target"))
			}
			continue
		}
		removeMutantsTree(repoRoot, tree)
	}
}

// mutantsWorktreeHeldBy reports whether any job in jobs is still using tree.
func mutantsWorktreeHeldBy(jobs []MutantsJob, tree string) bool {
	for _, j := range jobs {
		if j.Worktree == tree {
			return true
		}
	}
	return false
}

// removeMutantsTree drops one mutants worktree, its private Go clone (if
// any) and its lane marker. Unregistering the worktree before deleting it
// matters: it is a linked worktree, and a directory removed behind git's
// back leaves an entry that makes every later `worktree add` at that path
// fail.
func removeMutantsTree(repoRoot, tree string) {
	_, _ = git(repoRoot, "worktree", "remove", "--force", tree)
	_ = os.RemoveAll(tree)
	// The Go job runs in a private clone beside the tree rather than in
	// the linked worktree itself, so a lane leaves TWO full trees behind
	// and both have to go.
	_ = os.RemoveAll(goMutantsCloneDir(tree))
	_ = os.Remove(tree + mutantsLaneMarkerName)
}
