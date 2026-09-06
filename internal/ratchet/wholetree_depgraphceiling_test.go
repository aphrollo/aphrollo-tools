package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// depGraphCeilingLaw builds a dep-graph-ceiling law over roots, reusing
// metadataDoc's shape (defined in wholetree_test.go): server -> shared ->
// testrig and server -> movement (dev edge, never followed), client ->
// movement (normal edge).
func depGraphCeilingLaw(t *testing.T, root string, roots string, extra string) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "crate-fanout"
description = "a root may not reach more of the workspace than its baseline"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
roots = `+roots+`
`+extra+`
`, "crate-fanout")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

// TestDepGraphCeiling_CountsReachablePackagesNotJustForbiddenOnes proves the
// matcher's whole reason to exist: dep-graph-forbids over this same graph
// reports zero hits (nothing forbidden is named), but the ceiling still has
// a number to say — every reachable package counts, forbidden or not.
func TestDepGraphCeiling_CountsReachablePackagesNotJustForbiddenOnes(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), metadataDoc)

	hits, err := depGraphCeilingHits(root, depGraphCeilingLaw(t, root, `["server", "client"]`, ""))
	if err != nil {
		t.Fatalf("depGraphCeilingHits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want one per root", hits)
	}
	byKey := map[string]Hit{}
	for _, h := range hits {
		byKey[h.Key] = h
	}
	// server -> shared -> testrig, both normal edges; server -> movement is
	// a DEV edge and must not be followed, exactly like dep-graph-forbids.
	if got := byKey["server"].Weight; got != 2 {
		t.Errorf("server weight = %d, want 2 (shared, testrig — the dev edge to movement excluded)", got)
	}
	// client -> movement, one normal edge.
	if got := byKey["client"].Weight; got != 1 {
		t.Errorf("client weight = %d, want 1 (movement)", got)
	}
}

// TestDepGraphCeiling_RefusesToJudgeAWalkThatResolvedNothing mirrors
// dep-graph-forbids' own vacuity refusal: a root reaching nothing at all is
// a broken walk, not a ceiling of zero.
func TestDepGraphCeiling_RefusesToJudgeAWalkThatResolvedNothing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), `{
  "packages": [{"id": "s 1", "name": "server"}, {"id": "c 1", "name": "client"}],
  "resolve": {"nodes": [{"id": "s 1", "deps": []}, {"id": "c 1", "deps": []}]}}`)

	if _, err := depGraphCeilingHits(root, depGraphCeilingLaw(t, root, `["server"]`, "")); err == nil {
		t.Fatal("a vacuous walk must be an error, never a clean ceiling of zero")
	}
}

// TestDepGraphCeiling_MinReachableRefusesAWalkThatSawTooLittle mirrors
// dep-graph-forbids' wildcard vacuity floor.
func TestDepGraphCeiling_MinReachableRefusesAWalkThatSawTooLittle(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), metadataDoc)

	if _, err := depGraphCeilingHits(root, depGraphCeilingLaw(t, root, `"*"`, "min_reachable = 20")); err == nil {
		t.Fatal("a walk below its floor must fail loudly, never report clean")
	} else if !strings.Contains(err.Error(), "min_reachable") {
		t.Errorf("err = %v, want one naming the floor", err)
	}
	if _, err := depGraphCeilingHits(root, depGraphCeilingLaw(t, root, `"*"`, "min_reachable = 3")); err != nil {
		t.Fatalf("a walk at its floor is fine: %v", err)
	}
}

// thirdPartyMetadataDoc names an explicit workspace: "app" is the only
// workspace member, and it reaches both "sibling" (a workspace package NOT
// itself a root) and "serde" (third-party, outside workspace_members).
const thirdPartyMetadataDoc = `{
  "packages": [
    {"id": "a 1", "name": "app"},
    {"id": "b 1", "name": "sibling"},
    {"id": "d 1", "name": "serde"}
  ],
  "workspace_members": ["a 1", "b 1"],
  "resolve": {"nodes": [
    {"id": "a 1", "deps": [
      {"pkg": "b 1", "dep_kinds": [{"kind": null}]},
      {"pkg": "d 1", "dep_kinds": [{"kind": null}]}
    ]},
    {"id": "b 1", "deps": []},
    {"id": "d 1", "deps": []}
  ]}
}`

// TestDepGraphCeiling_CountsModeDistinguishesWorkspaceFromThirdParty proves
// both directions of the counts field: the DEFAULT ("workspace") excludes a
// reached third-party package from the count, and counts = "all" includes
// it — a trigger (reaching serde) is not by itself a bigger ceiling; the
// mode is what decides whether it counts.
func TestDepGraphCeiling_CountsModeDistinguishesWorkspaceFromThirdParty(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), thirdPartyMetadataDoc)

	workspaceHits, err := depGraphCeilingHits(root, depGraphCeilingLaw(t, root, `["app"]`, ""))
	if err != nil {
		t.Fatalf("depGraphCeilingHits: %v", err)
	}
	if len(workspaceHits) != 1 || workspaceHits[0].Weight != 1 {
		t.Fatalf("workspace-mode weight = %+v, want 1 (sibling only — serde is third-party)", workspaceHits)
	}

	allHits, err := depGraphCeilingHits(root, depGraphCeilingLaw(t, root, `["app"]`, `counts = "all"`))
	if err != nil {
		t.Fatalf("depGraphCeilingHits: %v", err)
	}
	if len(allHits) != 1 || allHits[0].Weight != 2 {
		t.Fatalf("all-mode weight = %+v, want 2 (sibling and serde both count)", allHits)
	}
}

// TestDepGraphCeiling_BaselineOnlyRegressesAboveTheCeiling is the ceiling's
// both-direction proof at the level a `deny` law actually judges it: a root
// that reaches no more than its baseline is clean (reaching packages ALONE
// is not a hit), and one edge past the baseline is a rejected regression.
func TestDepGraphCeiling_BaselineOnlyRegressesAboveTheCeiling(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), metadataDoc)
	writeLaw(t, root, "crate-fanout", `
name = "crate-fanout"
description = "a root may not reach more of the workspace than its baseline"
severity = "deny"
baseline = ".ratchet/baselines/crate_fanout.txt"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
roots = ["server", "client"]
`)

	// At the ceiling: server already reaches 2 (shared, testrig).
	write(t, filepath.Join(root, ".ratchet", "baselines", "crate_fanout.txt"), "client | 1\nserver | 2\n")
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("reaching exactly the baseline must not regress: %v", res.Lines())
	}

	// One edge past it: server's ceiling was recorded a package too low.
	write(t, filepath.Join(root, ".ratchet", "baselines", "crate_fanout.txt"), "client | 1\nserver | 1\n")
	res, err = Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %v, want exactly the server regression", res.Lines())
	}
	f := res.Findings[0]
	if f.Key != "server" || f.Baseline != 1 || f.Measured != 2 {
		t.Errorf("finding = %+v, want key=server baseline=1 measured=2", f)
	}
}
