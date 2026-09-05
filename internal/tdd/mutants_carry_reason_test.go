package tdd

import (
	"strings"
	"testing"
)

// measured is one stored outcome, already judged against a blob and a fence.
func measured(file, pkg, blob, fence string, line int) MutantOutcome {
	return MutantOutcome{
		File: file, Package: pkg, Blob: blob, Fence: fence,
		Line: line, Mutation: "replace + with -", Status: "caught",
	}
}

// stateOf is a TreeState naming one file's blob and its package's fence.
func stateOf(file, pkg, blob, fence string) TreeState {
	return TreeState{
		Blobs:    map[string]string{file: blob},
		Packages: map[string]string{file: pkg},
		Fences:   map[string]string{pkg: fence},
	}
}

// The question this answers is "why did my lane re-measure a file nobody
// touched", and until now nothing recorded an answer: a mutant that failed to
// carry left the plan indistinguishable from one that was never measured. The
// two have different fixes — a moved blob is the lane's own edit and correct
// to re-run, a moved fence is somebody else's test change invalidating a file
// this lane never opened.
func TestPlanMutants_SaysTheBlobMovedWhenTheFileItselfChanged(t *testing.T) {
	old := measured("a/x.rs", "a", "blob1", "fence1", 12)
	cached := map[mutantKey]MutantOutcome{old.key(): old}
	want := []MutantOutcome{measured("a/x.rs", "a", "", "", 12)}

	plan := PlanMutants(want, stateOf("a/x.rs", "a", "blob2", "fence1"), cached, "")

	if len(plan.Carry) != 0 {
		t.Fatalf("carried %d outcome(s) over a changed blob, want none", len(plan.Carry))
	}
	if len(plan.Skipped) != 1 {
		t.Fatalf("recorded %d skip reason(s), want exactly one for the one mutant that did not carry", len(plan.Skipped))
	}
	if got := plan.Skipped[0]; got.Reason != CarryBlobMoved {
		t.Fatalf("reason = %q, want %q — the file's own content changed", got.Reason, CarryBlobMoved)
	}
}

// The case that costs a lane an hour it did not earn: the file is untouched,
// and the package's test set moved because somebody else changed a test in it.
// The fence is package-wide, so one unrelated test edit re-measures every
// mutant in the package.
func TestPlanMutants_SaysTheFenceMovedWhenOnlyThePackagesTestsChanged(t *testing.T) {
	old := measured("a/x.rs", "a", "blob1", "fence1", 12)
	cached := map[mutantKey]MutantOutcome{old.key(): old}
	want := []MutantOutcome{measured("a/x.rs", "a", "", "", 12)}

	plan := PlanMutants(want, stateOf("a/x.rs", "a", "blob1", "fence2"), cached, "")

	if len(plan.Carry) != 0 {
		t.Fatalf("carried %d outcome(s) over a changed fence, want none", len(plan.Carry))
	}
	if len(plan.Skipped) != 1 {
		t.Fatalf("recorded %d skip reason(s), want one", len(plan.Skipped))
	}
	if got := plan.Skipped[0]; got.Reason != CarryFenceMoved {
		t.Fatalf("reason = %q, want %q — the file is byte-identical and its package's tests moved", got.Reason, CarryFenceMoved)
	}
}

// A mutant nobody has ever measured is not a carry failure, and counting it as
// one would hide the two reasons above behind first-run noise.
func TestPlanMutants_SaysUnmeasuredRatherThanBlamingABlobThatNeverHadAVerdict(t *testing.T) {
	want := []MutantOutcome{measured("a/x.rs", "a", "", "", 12)}

	plan := PlanMutants(want, stateOf("a/x.rs", "a", "blob1", "fence1"), map[mutantKey]MutantOutcome{}, "")

	if len(plan.Skipped) != 1 {
		t.Fatalf("recorded %d skip reason(s), want one", len(plan.Skipped))
	}
	if got := plan.Skipped[0]; got.Reason != CarryNeverMeasured {
		t.Fatalf("reason = %q, want %q", got.Reason, CarryNeverMeasured)
	}
}

// A carried mutant records nothing: the log is for what the run has to pay
// for, and a silent carry is the outcome that costs nothing.
func TestPlanMutants_RecordsNoReasonForAMutantThatCarried(t *testing.T) {
	old := measured("a/x.rs", "a", "blob1", "fence1", 12)
	cached := map[mutantKey]MutantOutcome{old.key(): old}
	want := []MutantOutcome{measured("a/x.rs", "a", "", "", 12)}

	plan := PlanMutants(want, stateOf("a/x.rs", "a", "blob1", "fence1"), cached, "")

	if len(plan.Carry) != 1 {
		t.Fatalf("carried %d, want the one unchanged mutant", len(plan.Carry))
	}
	if len(plan.Skipped) != 0 {
		t.Fatalf("recorded %d skip reason(s) for a clean carry, want none", len(plan.Skipped))
	}
}

// The operator-facing half: one line per FILE, not per mutant, or a package
// with 200 mutants drowns the log it is supposed to explain.
func TestCarrySkipSummary_IsOneLinePerFileNamingTheReasonAndTheCount(t *testing.T) {
	skipped := []CarrySkip{
		{File: "a/x.rs", Package: "a", Reason: CarryFenceMoved},
		{File: "a/x.rs", Package: "a", Reason: CarryFenceMoved},
		{File: "a/y.rs", Package: "a", Reason: CarryBlobMoved},
	}

	lines := CarrySkipSummary(skipped)

	if len(lines) != 2 {
		t.Fatalf("summary has %d line(s), want one per file: %v", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "a/x.rs") || !strings.Contains(joined, "a/y.rs") {
		t.Fatalf("summary does not name both files:\n%s", joined)
	}
	if !strings.Contains(joined, "2") {
		t.Fatalf("summary does not carry the per-file mutant count:\n%s", joined)
	}
	if !strings.Contains(joined, string(CarryFenceMoved)) || !strings.Contains(joined, string(CarryBlobMoved)) {
		t.Fatalf("summary does not name both reasons:\n%s", joined)
	}
}
