package tdd

import (
	"strings"
	"testing"
)

// A mutant in a.rs is caught by whatever test exercises it, and that test may
// reach it through a DIFFERENT crate. The first invalidation hash covered only
// the mutant's own file blob and its package's test blobs, so a change in a
// dependency — or in a sibling source file of the same package — left the old
// "caught" in place. For a gate, that is the dangerous direction: it reports a
// mutant as constrained by tests that no longer constrain it.
//
// listing is a `git ls-tree -r` fixture; the fence is computed over the
// package's own source AND test blobs plus every transitive workspace
// dependency's source blobs.
func fenceListing(coreBlob string) string {
	return strings.Join([]string{
		"100644 blob mA\tcrates/core/Cargo.toml",
		"100644 blob " + coreBlob + "\tcrates/core/src/lib.rs",
		"100644 blob tC\tcrates/core/tests/core_test.rs",
		"100644 blob mB\tcrates/app/Cargo.toml",
		"100644 blob sApp\tcrates/app/src/lib.rs",
		"100644 blob tApp\tcrates/app/tests/app_test.rs",
		"100644 blob mD\tcrates/other/Cargo.toml",
		"100644 blob sOther\tcrates/other/src/lib.rs",
	}, "\n")
}

// deps: app depends on core; other depends on nothing.
var fenceDeps = map[string][]string{"crates/app": {"crates/core"}}

func TestFence_ChangesWhenADependencyCrateChanges(t *testing.T) {
	t.Parallel()
	before := treeStateWithDeps(fenceListing("sCore"), fenceDeps)
	after := treeStateWithDeps(fenceListing("sCore-EDITED"), fenceDeps)

	if before.Fences["crates/app"] == after.Fences["crates/app"] {
		t.Fatal("app's fence survived an edit to the crate it depends on: a mutant in app carries a verdict its dependency can no longer justify")
	}
	if before.Fences["crates/other"] != after.Fences["crates/other"] {
		t.Fatal("an unrelated crate's fence moved: the invalidation is not scoped")
	}
}

// The package's own sibling source files count too: a mutant in a.rs may be
// caught only through b.rs's behaviour, and both live in one crate.
func TestFence_ChangesWhenASiblingSourceFileInTheSamePackageChanges(t *testing.T) {
	t.Parallel()
	before := treeStateWithDeps(fenceListing("sCore"), fenceDeps)
	edited := strings.Replace(fenceListing("sCore"), "blob sApp", "blob sApp-EDITED", 1)
	after := treeStateWithDeps(edited, fenceDeps)

	if before.Fences["crates/app"] == after.Fences["crates/app"] {
		t.Fatal("a source change inside the package left its own fence unmoved")
	}
}

// A test file still invalidates, which is what the fence replaced.
func TestFence_ChangesWhenThePackagesTestSetChanges(t *testing.T) {
	t.Parallel()
	before := treeStateWithDeps(fenceListing("sCore"), fenceDeps)
	edited := strings.Replace(fenceListing("sCore"), "blob tApp", "blob tApp-EDITED", 1)
	if before.Fences["crates/app"] == treeStateWithDeps(edited, fenceDeps).Fences["crates/app"] {
		t.Fatal("an edited test file left the fence unmoved")
	}
}

// Transitively: app -> mid -> core.
func TestFence_FollowsTheDependencyGraphTransitively(t *testing.T) {
	t.Parallel()
	listing := func(coreBlob string) string {
		return strings.Join([]string{
			"100644 blob mA\tcrates/core/Cargo.toml",
			"100644 blob " + coreBlob + "\tcrates/core/src/lib.rs",
			"100644 blob mM\tcrates/mid/Cargo.toml",
			"100644 blob sMid\tcrates/mid/src/lib.rs",
			"100644 blob mB\tcrates/app/Cargo.toml",
			"100644 blob sApp\tcrates/app/src/lib.rs",
		}, "\n")
	}
	deps := map[string][]string{"crates/app": {"crates/mid"}, "crates/mid": {"crates/core"}}
	if treeStateWithDeps(listing("sCore"), deps).Fences["crates/app"] ==
		treeStateWithDeps(listing("sCore-EDITED"), deps).Fences["crates/app"] {
		t.Fatal("app's fence ignored a change two crates down its dependency chain")
	}
}

// A cyclic or self-referential graph must not hang the plan.
func TestFence_TerminatesOnACycle(t *testing.T) {
	t.Parallel()
	deps := map[string][]string{"crates/app": {"crates/core"}, "crates/core": {"crates/app"}}
	st := treeStateWithDeps(fenceListing("sCore"), deps)
	if st.Fences["crates/app"] == "" {
		t.Fatal("no fence was computed for a package in a dependency cycle")
	}
}

// Cargo's own answer, parsed from `cargo metadata --no-deps`: only the
// workspace's path dependencies are edges, because a registry crate cannot
// change under a lane.
func TestCargoWorkspaceDeps_ReadsPathDependenciesFromMetadata(t *testing.T) {
	t.Parallel()
	meta := `{"packages":[
	  {"name":"app","manifest_path":"/repo/crates/app/Cargo.toml","dependencies":[
	    {"name":"core","path":"/repo/crates/core"},
	    {"name":"serde"}]},
	  {"name":"core","manifest_path":"/repo/crates/core/Cargo.toml","dependencies":[]}
	]}`
	deps, err := parseCargoWorkspaceDeps([]byte(meta), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got := deps["crates/app"]; len(got) != 1 || got[0] != "crates/core" {
		t.Fatalf("app's deps = %v, want the path dependency only (serde cannot change under a lane)", got)
	}
}

// The lane's receipt must describe the LANE. Planning the carry over the whole
// store stamped it with outcomes for every unchanged file in the repo, so
// mutants_total stopped meaning anything about the commit.
func TestScopeCarry_IsLimitedToTheLanesOwnFiles(t *testing.T) {
	t.Parallel()
	cached := cachedOutcomes([]MutantOutcome{
		{File: "crates/app/src/lib.rs", Line: 1, Col: 5, Mutation: "m", Package: "crates/app", Blob: "sApp", Fence: "f1", Status: "caught"},
		{File: "crates/other/src/lib.rs", Line: 2, Col: 5, Mutation: "m", Package: "crates/other", Blob: "sOther", Fence: "f2", Status: "caught"},
	})
	got := laneWants(cached, []string{"crates/app/src/lib.rs"})
	if len(got) != 1 || got[0].File != "crates/app/src/lib.rs" {
		t.Fatalf("carry candidates = %+v, want only the lane's own file", got)
	}
}

// A file that MOVED is the same content at a new path, and its verdicts are
// facts about that content. Keyed on the path, every one of them was thrown
// away by a rename — the most expensive possible re-measure for the least
// possible change.
func TestPlanMutants_CarriesAnOutcomeAcrossARename(t *testing.T) {
	t.Parallel()
	measured := MutantOutcome{File: "crates/a/src/old_name.rs", Line: 12, Col: 5,
		Mutation: "replace > with >= in f", Package: "crates/a",
		Blob: "sameblob", Fence: "samefence", Status: "caught"}
	cached := cachedOutcomes([]MutantOutcome{measured})

	// The same mutant, now listed under the file's new path.
	want := MutantOutcome{File: "crates/a/src/new_name.rs", Line: 12, Col: 5,
		Mutation: "replace > with >= in f", Package: "crates/a"}
	now := TreeState{
		Blobs:    map[string]string{"crates/a/src/new_name.rs": "sameblob"},
		Packages: map[string]string{"crates/a/src/new_name.rs": "crates/a"},
		Fences:   map[string]string{"crates/a": "samefence"},
	}

	plan := PlanMutants([]MutantOutcome{want}, now, cached, "")
	if len(plan.Run) != 0 {
		t.Fatalf("Run = %+v, want the renamed file's verdict reused", plan.Run)
	}
	if len(plan.Carry) != 1 || plan.Carry[0].Status != "caught" {
		t.Fatalf("Carry = %+v, want the measured verdict", plan.Carry)
	}
	if plan.Carry[0].File != "crates/a/src/new_name.rs" {
		t.Fatalf("carried outcome names %q, want the file's CURRENT path", plan.Carry[0].File)
	}
}

// Same content, different fence — the crate it landed in has other tests — is
// not a carry: the code that catches it is not the code that caught it.
func TestPlanMutants_DoesNotCarryAcrossAMoveIntoAnotherPackage(t *testing.T) {
	t.Parallel()
	cached := cachedOutcomes([]MutantOutcome{{File: "crates/a/src/x.rs", Line: 12, Col: 5,
		Mutation: "replace > with >= in f", Package: "crates/a",
		Blob: "sameblob", Fence: "fence-a", Status: "caught"}})

	want := MutantOutcome{File: "crates/b/src/x.rs", Line: 12, Col: 5,
		Mutation: "replace > with >= in f", Package: "crates/b"}
	now := TreeState{
		Blobs:    map[string]string{"crates/b/src/x.rs": "sameblob"},
		Packages: map[string]string{"crates/b/src/x.rs": "crates/b"},
		Fences:   map[string]string{"crates/b": "fence-b"},
	}

	if plan := PlanMutants([]MutantOutcome{want}, now, cached, ""); len(plan.Carry) != 0 {
		t.Fatalf("Carry = %+v, want a move into a differently fenced package re-measured", plan.Carry)
	}
}
