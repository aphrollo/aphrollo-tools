package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// Each of MutantsInvocation's four fields is verdict-affecting on its own:
// a mutation that dropped any one of them from Version()'s format string
// would let two genuinely different invocations stamp the identical value,
// silently carrying an outcome measured under the OLD flag forward as if
// nothing had changed (issue #531's own failure mode).
func TestMutantsInvocation_Version_DiffersOnEachVerdictAffectingField(t *testing.T) {
	base := MutantsInvocation{TestTool: "nextest"}
	baseVersion := base.Version()

	variants := []struct {
		name string
		inv  MutantsInvocation
	}{
		{"test tool", MutantsInvocation{TestTool: "go test"}},
		{"run-ignored", MutantsInvocation{TestTool: "nextest", RunIgnored: "all"}},
		{"nextest profile", MutantsInvocation{TestTool: "nextest", NextestProfile: "gate"}},
		{"database provisioned", MutantsInvocation{TestTool: "nextest", DatabaseProvisioned: true}},
	}
	for _, v := range variants {
		if got := v.inv.Version(); got == baseVersion {
			t.Fatalf("%s: Version() = %q, want it to differ from the base invocation %q", v.name, got, baseVersion)
		}
	}
}

// Two runs of the SAME repo, scoped to different diffs and different
// package lists, must stamp the SAME InvocationVersion: MutantsArgv's own
// --in-diff is a temp path that differs every run and --package narrows
// what is measured, never HOW. Hashing either into the invocation would
// invalidate every cached outcome on every run — "strictly worse than not
// having the field at all" (issue #531).
func TestMutantsInvocationVersion_SameAcrossDifferentDiffsAndPackageLists(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(filepath.Join(root, "tools", "mutation_gate.sh"), "#!/bin/sh\n")

	// Two runs MutantsArgv itself renders very differently...
	argvA := MutantsArgv(filepath.Join(t.TempDir(), "a.diff"), true, nil, []string{"crates/a"}, "")
	argvB := MutantsArgv(filepath.Join(t.TempDir(), "b.diff"), true, nil, []string{"crates/a", "crates/b", "crates/c"}, "")
	if strings.Join(argvA, " ") == strings.Join(argvB, " ") {
		t.Fatalf("test setup: the two argvs must actually differ in --in-diff/--package, both rendered %v", argvA)
	}

	// ...must still be the same invocation: mutantsInvocationVersion has no
	// way to see a diff path or a package list at all.
	const producerVersion = "cargo-mutants 27.1.0"
	if a, b := mutantsInvocationVersion(root, producerVersion), mutantsInvocationVersion(root, producerVersion); a != b {
		t.Fatalf("mutantsInvocationVersion = %q then %q, want the same repo to stamp the same invocation regardless of a run's diff/package scope", a, b)
	}
}

// A verdict-affecting difference — the test tool a Cargo workspace vs a Go
// module drives its suite with — must produce a DIFFERENT InvocationVersion:
// an outcome measured under nextest and one measured under `go test` are not
// interchangeable answers to "does this mutant's verdict still hold".
func TestMutantsInvocationVersion_DiffersWhenTheTestToolDiffers(t *testing.T) {
	cargoRoot := t.TempDir()
	mustWriteFile(filepath.Join(cargoRoot, "tools", "mutation_gate.sh"), "#!/bin/sh\n")

	goRoot := t.TempDir()
	mustWriteFile(filepath.Join(goRoot, "go.mod"), "module example\n")

	cargoVersion := mutantsInvocationVersion(cargoRoot, "cargo-mutants 27.1.0")
	goVersion := mutantsInvocationVersion(goRoot, "gremlins version 0.7.0")
	if cargoVersion == goVersion {
		t.Fatalf("mutantsInvocationVersion = %q for both a Cargo and a Go worktree, want the test tool to tell them apart", cargoVersion)
	}
}

// A producer this box could not even confirm runs (mutantsProducerVersion's
// own "" — no producer detected, or its version query itself errored) means
// invocation is equally unknowable, even in a worktree that plainly carries
// the Cargo manifest a real run would use nextest against. Matching
// ProducerVersion's own degrade this way — rather than deciding
// determinability purely from which manifest file is present — is what
// keeps a pre-existing cached outcome with no InvocationVersion at all
// carrying forward in exactly the environments (this package's own test
// fixtures among them) where the real producer binary cannot be queried,
// instead of a mass one-time re-measure of the whole fleet's cache the
// moment this field first ships.
func TestMutantsInvocationVersion_EmptyWhenTheProducerVersionIsEmpty(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(filepath.Join(root, "tools", "mutation_gate.sh"), "#!/bin/sh\n")

	if got := mutantsInvocationVersion(root, ""); got != "" {
		t.Fatalf("mutantsInvocationVersion(root, \"\") = %q, want empty when the producer version could not be determined", got)
	}
}

// stampInvocationVersion must not mutate the caller's own slice, the same
// contract stampProducerVersion already carries: the gremlins job path
// reuses its outcomes slice for the signed receipt right after stamping it.
func TestStampInvocationVersion_LeavesTheInputSliceUntouched(t *testing.T) {
	in := []MutantOutcome{{File: "a.rs", Line: 1, Mutation: "m"}}
	out := stampInvocationVersion(in, TreeState{}, constInvocation("test-tool=go test run-ignored= nextest-profile= db=false"))

	if in[0].InvocationVersion != "" {
		t.Fatalf("input outcome mutated in place: %+v", in[0])
	}
	if len(out) != 1 || out[0].InvocationVersion == "" {
		t.Fatalf("stampInvocationVersion output = %+v, want InvocationVersion stamped", out)
	}
}

// The mutant-level mirror of
// TestPlanMutants_DoesNotCarryAMutantMeasuredUnderADifferentProducerVersion:
// carriesOver reads an invocation mismatch the same way, by plain equality,
// with no special-casing beyond what ProducerVersion already established.
func TestPlanMutants_DoesNotCarryAMutantMeasuredUnderADifferentInvocationVersion(t *testing.T) {
	const oldInvocation = "test-tool=nextest run-ignored= nextest-profile= db=false"
	const newInvocation = "test-tool=nextest run-ignored=all nextest-profile= db=true"
	prev := cachedOutcomes([]MutantOutcome{
		{File: "crates/a/src/lib.rs", Line: 12, Mutation: "replace + with -", Package: "a",
			Blob: "blobA", Fence: "tsA", Status: "caught", InvocationVersion: oldInvocation},
	})
	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA"}, map[string]string{"a": "tsA"}),
		prev, "", constInvocation(newInvocation))

	if len(plan.Run) != 1 || len(plan.Carry) != 0 {
		t.Fatalf("Run = %+v, Carry = %+v, want the mutant re-run under the new invocation, not carried under the old one",
			plan.Run, plan.Carry)
	}

	// The SAME invocation still carries — this is not "always re-measure",
	// only an invocation change forces it.
	plan = PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA"}, map[string]string{"a": "tsA"}),
		prev, "", constInvocation(oldInvocation))
	if len(plan.Run) != 0 || len(plan.Carry) != 1 {
		t.Fatalf("Run = %+v, Carry = %+v, want the mutant carried when the invocation has not changed",
			plan.Run, plan.Carry)
	}
}

// The point of #531's per-package constraint: two packages measured in the
// SAME run, where only one of them actually sits behind a verdict-affecting
// invocation change (a database tier active for one package and not the
// other). A run-wide InvocationVersion could not express this at all — it
// would stamp the SAME string onto both packages, either re-measuring the
// package nothing changed for or, worse, silently carrying the package the
// flag change actually affects. PlanMutants must resolve each mutant's
// invocation against ITS OWN package and judge carry independently.
func TestPlanMutants_CarriesOnePackageWhileReRunningAnotherUnderADifferentInvocation(t *testing.T) {
	const dbOff = "test-tool=nextest run-ignored= nextest-profile= db=false"
	const dbOn = "test-tool=nextest run-ignored= nextest-profile= db=true"

	// Package "a" was last measured with the database tier off and still is:
	// nothing about its invocation changed. Package "b" was last measured
	// with the tier off too, but this run turns it on for "b" alone.
	prev := cachedOutcomes([]MutantOutcome{
		{File: "crates/a/src/lib.rs", Line: 12, Mutation: "replace + with -", Package: "a",
			Blob: "blobA", Fence: "tsA", Status: "caught", InvocationVersion: dbOff},
		{File: "crates/b/src/lib.rs", Line: 3, Mutation: "replace + with -", Package: "b",
			Blob: "blobB", Fence: "tsB", Status: "caught", InvocationVersion: dbOff},
	})

	perPackage := func(pkg string) string {
		if pkg == "b" {
			return dbOn
		}
		return dbOff
	}

	plan := PlanMutants(
		[]MutantOutcome{want("crates/a/src/lib.rs", 12, "a"), want("crates/b/src/lib.rs", 3, "b")},
		state(map[string]string{"crates/a/src/lib.rs": "blobA", "crates/b/src/lib.rs": "blobB"},
			map[string]string{"a": "tsA", "b": "tsB"}),
		prev, "", perPackage)

	if len(plan.Carry) != 1 || plan.Carry[0].Package != "a" {
		t.Fatalf("Carry = %+v, want only package a's outcome carried — its invocation did not change", plan.Carry)
	}
	if len(plan.Run) != 1 || plan.Run[0].Package != "b" {
		t.Fatalf("Run = %+v, want package b re-run under its new, database-on invocation", plan.Run)
	}
}
