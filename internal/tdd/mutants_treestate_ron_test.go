package tdd

import (
	"testing"
)

// PlanDiffFiles decides what the RUNNER measures; laneHasNothingToMutate
// (mutants_carry.go) decides whether the MERGE GATE requires a receipt at
// all — and both have to agree on one file's classification, or a lane can
// be told "nothing to judge" by one and "go measure this" by the other. A
// bare .ron has to resolve its owning crate from the FILESYSTEM (see
// classifyRepoPath's comment), so it is the one extension where the two
// callers can land on different verdicts if one of them resolves it against
// the wrong directory.
//
// This proves that divergence: PlanDiffFiles used to classify with the bare,
// CWD-relative ClassifyFile rather than the repo-root-anchored
// classifyRepoPath laneHasNothingToMutate uses, so a process whose working
// directory is not the lane's own repo root could pick up an unrelated
// Cargo.toml sitting above its CWD and mistake a .ron that belongs to NO
// crate in the real repo for one that does.
func TestPlanDiffFiles_AgreesWithLaneHasNothingToMutateOnAnUnownedRonFile(t *testing.T) {
	repoRoot := t.TempDir()
	write(t, repoRoot, "assets/foo.ron", "(kind: Test)\n")

	// A second, unrelated tree whose OWN root carries a real Cargo.toml.
	// Nothing in repoRoot is under it — the disagreement this proves is that
	// PlanDiffFiles must not go looking there at all.
	elsewhere := t.TempDir()
	write(t, elsewhere, "Cargo.toml", "[package]\nname = \"unrelated\"\n")
	t.Chdir(elsewhere)

	if got := classifyRepoPath(repoRoot, "assets/foo.ron"); got != Ignore {
		t.Fatalf("classifyRepoPath = %v, want Ignore — repoRoot owns no crate over assets/foo.ron", got)
	}

	now := TreeState{Blobs: map[string]string{"assets/foo.ron": "b1"}}
	got := PlanDiffFiles(repoRoot, []string{"assets/foo.ron"}, now, nil)
	if len(got) != 0 {
		t.Fatalf("PlanDiffFiles = %v, want none — it must classify assets/foo.ron the same way "+
			"laneHasNothingToMutate just did, not against whatever crate happens to sit above the "+
			"current process's working directory", got)
	}
}
