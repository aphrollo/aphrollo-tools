package tdd

import "testing"

// outcome is one recorded mutant, as a receipt carries it.
func outcome(file string, line int, pkg, blob, testSet, status string) MutantOutcome {
	return MutantOutcome{File: file, Line: line, Mutation: "replace + with -",
		Package: pkg, Blob: blob, TestSet: testSet, Status: status}
}

// want is the same mutant as the current run's list names it: no result yet,
// and no measurement of its own — the plan supplies both from the tree.
func want(file string, line int, pkg string) MutantOutcome {
	return MutantOutcome{File: file, Line: line, Mutation: "replace + with -", Package: pkg}
}

func state(blobs, testSets map[string]string) TreeState {
	return TreeState{Blobs: blobs, TestSets: testSets}
}

// cachedOutcomes is the repo-wide store as the plan reads it: outcomes keyed
// by the mutant they describe.
func cachedOutcomes(out []MutantOutcome) map[mutantKey]MutantOutcome {
	byKey := map[mutantKey]MutantOutcome{}
	for _, m := range out {
		byKey[m.key()] = m
	}
	return byKey
}

// A second run on the same lane must not re-mutate a crate nothing touched.
// The outcome was measured against this exact file blob and this exact
// test-set hash, so it is still true, and re-measuring it is the cold build
// the incremental plan exists to avoid.
func TestPlanMutants_CarriesAnUnchangedFilesOutcomes(t *testing.T) {
	prev := cachedOutcomes([]MutantOutcome{
		outcome("crates/a/src/lib.rs", 12, "a", "blobA", "tsA", "caught"),
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA"}, map[string]string{"a": "tsA"}),
		prev)

	if len(plan.Run) != 0 {
		t.Fatalf("Run = %v, want nothing to re-run for an untouched crate", plan.Run)
	}
	if len(plan.Carry) != 1 || plan.Carry[0].Status != "caught" {
		t.Fatalf("Carry = %+v, want the recorded outcome carried", plan.Carry)
	}
}

// A changed test file re-runs its own crate's mutants even where the source
// file is byte-identical: the new test may catch a mutant the old test set
// missed, so every outcome in that package is stale.
func TestPlanMutants_RerunsAPackageWhoseTestSetHashChanged(t *testing.T) {
	prev := cachedOutcomes([]MutantOutcome{
		outcome("crates/a/src/lib.rs", 12, "a", "blobA", "tsA", "caught"),
		outcome("crates/b/src/lib.rs", 3, "b", "blobB", "tsB", "caught"),
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a"), want("crates/b/src/lib.rs", 3, "b")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA", "crates/b/src/lib.rs": "blobB"},
			map[string]string{"a": "tsA-NEW", "b": "tsB"}),
		prev)

	if len(plan.Run) != 1 || plan.Run[0].Package != "a" {
		t.Fatalf("Run = %+v, want only crate a's mutants re-run", plan.Run)
	}
	if len(plan.Carry) != 1 || plan.Carry[0].Package != "b" {
		t.Fatalf("Carry = %+v, want crate b's outcome carried", plan.Carry)
	}
}

// The file the mutant lives in changed, so the outcome describes source that
// is no longer there.
func TestPlanMutants_RunsAMutantWhoseFileBlobChanged(t *testing.T) {
	prev := cachedOutcomes([]MutantOutcome{
		outcome("crates/a/src/lib.rs", 12, "a", "blobA", "tsA", "caught"),
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA-NEW"}, map[string]string{"a": "tsA"}),
		prev)

	if len(plan.Run) != 1 || len(plan.Carry) != 0 {
		t.Fatalf("Run = %+v, Carry = %+v, want the changed file's mutant re-run", plan.Run, plan.Carry)
	}
}

// A mutant the previous run never saw (a line the lane just added) has no
// outcome to carry, whatever the rest of the file did.
func TestPlanMutants_RunsAMutantThePreviousRunNeverMeasured(t *testing.T) {
	prev := cachedOutcomes([]MutantOutcome{
		outcome("crates/a/src/lib.rs", 12, "a", "blobA", "tsA", "caught"),
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 40, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA"}, map[string]string{"a": "tsA"}),
		prev)

	if len(plan.Run) != 1 || plan.Run[0].Line != 40 {
		t.Fatalf("Run = %+v, want the unmeasured mutant to run", plan.Run)
	}
}

// An older producer's receipt records outcomes with no blob and no test-set
// hash. Nothing about them is checkable, so nothing carries: a plan that
// trusted a blank measurement would report an untested mutant as caught.
func TestPlanMutants_RunsEverythingWhenThePreviousReceiptRecordsNoBlobs(t *testing.T) {
	prev := cachedOutcomes([]MutantOutcome{
		{File: "crates/a/src/lib.rs", Line: 12, Mutation: "replace + with -", Status: "caught"},
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA"}, map[string]string{"a": "tsA"}),
		prev)

	if len(plan.Run) != 1 || len(plan.Carry) != 0 {
		t.Fatalf("Run = %+v, Carry = %+v, want an unmeasured receipt to carry nothing", plan.Run, plan.Carry)
	}
}

// With no previous receipt at all every mutant runs, and the plan says so
// without dereferencing anything.
func TestPlanMutants_RunsEverythingWithNoPreviousReceipt(t *testing.T) {
	plan := PlanMutants([]MutantOutcome{want("crates/a/src/lib.rs", 12, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA"}, map[string]string{"a": "tsA"}), nil)
	if len(plan.Run) != 1 || len(plan.Carry) != 0 {
		t.Fatalf("Run = %+v, Carry = %+v, want a first run to measure everything", plan.Run, plan.Carry)
	}
}

// The plan stamps what it measured onto every entry it hands back, both the
// ones it will run and the ones it carries: the next receipt is what the run
// after this one compares against, so an entry that kept a stale blob would
// carry forever.
func TestPlanMutants_StampsTheMeasurementItJudgedAgainst(t *testing.T) {
	prev := cachedOutcomes([]MutantOutcome{
		outcome("crates/a/src/lib.rs", 12, "a", "blobA", "tsA", "caught"),
		outcome("crates/b/src/lib.rs", 3, "b", "blobB-OLD", "tsB", "caught"),
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a"), want("crates/b/src/lib.rs", 3, "b")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA", "crates/b/src/lib.rs": "blobB"},
			map[string]string{"a": "tsA", "b": "tsB"}),
		prev)

	for _, m := range append(append([]MutantOutcome{}, plan.Run...), plan.Carry...) {
		if m.Blob != map[string]string{"a": "blobA", "b": "blobB"}[m.Package] {
			t.Fatalf("%s carries blob %q, want the blob the plan judged it against", m.Package, m.Blob)
		}
		if m.TestSet == "" {
			t.Fatalf("%s carries no test-set hash", m.Package)
		}
	}
}
