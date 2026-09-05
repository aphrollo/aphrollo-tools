package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// One mutants worktree per lane keeps two runs from checking a tree out from
// under each other. The build directory did not need to follow it: a per-lane
// target dir made every lane cold-build the whole dependency graph once and
// hold 8-18 GB for it. Measured on borld 2026-09-05: nine per-lane target dirs
// of 8.3-18.4 GB each, and across 44 runs the unmutated baseline builds cost
// 180 min against 116 min for every per-mutant rebuild put together. The
// collision a shared target dir risked — two producers linking into one
// directory — is already impossible: the box-wide mutation-run lock serializes
// every producer on the machine, its build included. Cargo keys a workspace
// crate's artifacts on the path it was compiled from, so two lanes sharing one
// target dir share the dependency graph and keep their own crates apart.
func TestMutantsTargetDir_IsOnePerRepoSharedByEveryLane(t *testing.T) {
	root := makeGoRepo(t)
	laneA := addWorktree(t, root, "lane-a")
	laneB := addWorktree(t, root, "lane-b")

	ta, tb := MutantsTargetDir(laneA), MutantsTargetDir(laneB)
	if ta != tb {
		t.Fatalf("MutantsTargetDir(lane-a) = %q, MutantsTargetDir(lane-b) = %q — two target dirs cold-build the dependency graph twice", ta, tb)
	}
	want := filepath.Join(MutantsRootDir(root), "target")
	if ta != want {
		t.Fatalf("MutantsTargetDir = %q, want the repo's one %q", ta, want)
	}
	// It stays under the repo's mutants root, which is what the build-slot
	// bypass keys on: a target dir anywhere else queues like every other build.
	for _, lane := range []string{laneA, laneB} {
		if strings.HasPrefix(ta, MutantsWorktreeDir(lane)+string(filepath.Separator)) {
			t.Errorf("MutantsTargetDir = %q sits inside lane worktree %q — reclaiming that lane's tree would throw the warm build away", ta, MutantsWorktreeDir(lane))
		}
	}
}
