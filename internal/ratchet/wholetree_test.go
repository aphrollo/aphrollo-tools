package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// metadataDoc is a resolved graph in cargo's own shape: server -> shared ->
// testrig (a NORMAL edge, the defect) and server -> movement as a DEV edge
// (legal, and never followed).
const metadataDoc = `{
  "packages": [
    {"id": "s 1", "name": "server"},
    {"id": "c 1", "name": "client"},
    {"id": "h 1", "name": "shared"},
    {"id": "t 1", "name": "testrig"},
    {"id": "m 1", "name": "movement"},
    {"id": "e 1", "name": "editor_wire"}
  ],
  "resolve": {"nodes": [
    {"id": "s 1", "deps": [
      {"pkg": "h 1", "dep_kinds": [{"kind": null}]},
      {"pkg": "m 1", "dep_kinds": [{"kind": "dev"}]}
    ]},
    {"id": "h 1", "deps": [{"pkg": "t 1", "dep_kinds": [{"kind": null}]}]},
    {"id": "c 1", "deps": [{"pkg": "m 1", "dep_kinds": [{"kind": null}]}]},
    {"id": "t 1", "deps": []},
    {"id": "m 1", "deps": []},
    {"id": "e 1", "deps": []}
  ]}
}`

func depGraphLaw(t *testing.T, root string) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "prod-graph"
description = "dev-only tooling never reaches a shipping binary"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-forbids"
roots = ["server", "client"]
forbidden = ["editor*", "testrig"]
edges = "normal"
`, "prod-graph")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

func TestDepGraphFindsATransitiveForbiddenEdgeAndNamesThePath(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), metadataDoc)

	hits, err := depGraphHits(root, depGraphLaw(t, root))
	if err != nil {
		t.Fatalf("depGraphHits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", keys(hits))
	}
	if hits[0].Key != "server->shared->testrig" {
		t.Errorf("key = %q — the path is what an edge gets deleted from", hits[0].Key)
	}
}

// A dev-dependency never reaches the shipping binary; following it would blind
// the walk to the one distinction the rule is about.
func TestDepGraphDoesNotFollowADevEdge(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), strings.Replace(metadataDoc,
		`{"pkg": "t 1", "dep_kinds": [{"kind": null}]}`,
		`{"pkg": "t 1", "dep_kinds": [{"kind": "dev"}]}`, 1))

	hits, err := depGraphHits(root, depGraphLaw(t, root))
	if err != nil {
		t.Fatalf("depGraphHits: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("a dev edge is the legal shape: %v", keys(hits))
	}
}

func TestDepGraphGlobMatchesAFamilyOfForbiddenPackages(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), strings.Replace(metadataDoc,
		`{"id": "c 1", "deps": [{"pkg": "m 1", "dep_kinds": [{"kind": null}]}]}`,
		`{"id": "c 1", "deps": [{"pkg": "m 1", "dep_kinds": [{"kind": null}]}, {"pkg": "e 1", "dep_kinds": [{"kind": null}]}]}`, 1))

	hits, err := depGraphHits(root, depGraphLaw(t, root))
	if err != nil {
		t.Fatalf("depGraphHits: %v", err)
	}
	joined := strings.Join(keys(hits), " ")
	if !strings.Contains(joined, "client->editor_wire") {
		t.Errorf("`editor*` must match editor_wire: %v", keys(hits))
	}
}

// A walk that resolved nothing satisfies "reaches no forbidden package"
// perfectly — over no data at all.
func TestDepGraphRefusesToJudgeAWalkThatResolvedNothing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), `{
  "packages": [{"id": "s 1", "name": "server"}, {"id": "c 1", "name": "client"}],
  "resolve": {"nodes": [{"id": "s 1", "deps": []}, {"id": "c 1", "deps": []}]}}`)

	if _, err := depGraphHits(root, depGraphLaw(t, root)); err == nil {
		t.Fatal("a vacuous walk must be an error, never a clean verdict")
	}
}

func containmentLaw(t *testing.T, root string, escape string) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "stub-agreement"
description = "a stand-in refuses at least what the real system refuses"
severity = "deny"
escape = "`+escape+`"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "file-set-containment"
superset_file = "crates/testrig/src/visuals.rs"
subset_file = "crates/client/src/render/attach.rs"
capture = "Without<([A-Za-z_]+)>"
`, "stub-agreement")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

func containmentTree(t *testing.T, stub string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "crates", "client", "src", "render", "attach.rs"),
		"fn attach(q: Query<&PlayerPosition, (Without<SimBody>, Without<PropShape>, Without<DropView>)>) {}\n")
	write(t, filepath.Join(root, "crates", "testrig", "src", "visuals.rs"), stub)
	return root
}

func TestContainmentNamesTheFilterTheStandInIsMissing(t *testing.T) {
	root := containmentTree(t,
		"fn attach_stub(q: Query<&PlayerPosition, (Without<SimBody>, Without<PropShape>)>) {}\n")

	hits, err := containmentHits(root, containmentLaw(t, root, "// substitute-ok:"))
	if err != nil {
		t.Fatalf("containmentHits: %v", err)
	}
	if len(hits) != 1 || !strings.HasSuffix(hits[0].Key, "| DropView") {
		t.Fatalf("hits = %v", keys(hits))
	}
}

func TestContainmentAllowsAStandInThatRefusesMore(t *testing.T) {
	root := containmentTree(t,
		"fn attach_stub(q: Query<&PlayerPosition, (Without<SimBody>, Without<PropShape>, Without<DropView>, Without<Ghost>)>) {}\n")

	hits, err := containmentHits(root, containmentLaw(t, root, "// substitute-ok:"))
	if err != nil {
		t.Fatalf("containmentHits: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("refusing MORE is the legal direction: %v", keys(hits))
	}
}

func TestContainmentWaiverIsHonouredAndGoesStale(t *testing.T) {
	waived := containmentTree(t,
		"// substitute-ok: the rig has no drops\nfn attach_stub(q: Query<&PlayerPosition, (Without<SimBody>)>) {}\n")
	hits, err := containmentHits(waived, containmentLaw(t, waived, "// substitute-ok:"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("a deliberate waiver must be honoured: %v", keys(hits))
	}

	stale := containmentTree(t,
		"// substitute-ok: stale\nfn attach_stub(q: Query<&PlayerPosition, (Without<SimBody>, Without<PropShape>, Without<DropView>)>) {}\n")
	hits, err = containmentHits(stale, containmentLaw(t, stale, "// substitute-ok:"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].What, "stale") {
		t.Fatalf("a waiver with nothing left to waive is itself a finding: %v", hits)
	}
}

func TestContainmentRefusesAVacuousComparison(t *testing.T) {
	root := containmentTree(t, "fn attach_stub() {}\n")
	write(t, filepath.Join(root, "crates", "client", "src", "render", "attach.rs"), "fn attach() {}\n")
	if _, err := containmentHits(root, containmentLaw(t, root, "")); err == nil {
		t.Fatal("empty contains empty — that must be an error, not a pass")
	}
}

func jsonCeilingLaw(t *testing.T, root string) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "perf"
description = "a tier-1 kernel bench may not regress"
severity = "deny"
baseline = ".ratchet/baselines/perf.txt"

[scope]
include = ["**/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "criterion/**/new/estimates.json"
path = "mean.point_estimate"
tolerance_pct = 5
enabled_env = "BORLD_PERF_RATCHET"
`, "perf")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

func criterionTree(t *testing.T, ns string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "criterion", "apply_movement", "grounded", "new", "estimates.json"),
		`{"mean": {"point_estimate": `+ns+`, "standard_error": 1.0}}`)
	return root
}

func TestJSONCeilingReadsTheNumberAndKeysItByBenchID(t *testing.T) {
	root := criterionTree(t, "46.3")
	hits, err := jsonCeilingHits(root, jsonCeilingLaw(t, root), true, "")
	if err != nil {
		t.Fatalf("jsonCeilingHits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %v", keys(hits))
	}
	if hits[0].Key != "criterion/apply_movement/grounded" {
		t.Errorf("key = %q, want the bench id", hits[0].Key)
	}
	if hits[0].Weight != 47 {
		t.Errorf("weight = %d — a ceiling rounds up", hits[0].Weight)
	}
}

func TestJSONCeilingErrorsWhenArmedOverNothing(t *testing.T) {
	if _, err := jsonCeilingHits(t.TempDir(), jsonCeilingLaw(t, t.TempDir()), true, ""); err == nil {
		t.Fatal("armed with no data must be an error, never a pass")
	}
}

func TestJSONCeilingErrorsOnAMissingPath(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "criterion", "b", "c", "new", "estimates.json"), `{"median": {"point_estimate": 1}}`)
	if _, err := jsonCeilingHits(root, jsonCeilingLaw(t, root), true, ""); err == nil {
		t.Fatal("a path that names nothing must fail loudly")
	}
}

// The tolerance belongs to the COMPARISON: a value inside it still has to
// lower its ceiling, so the hit is emitted either way.
func TestJSONCeilingToleranceIsAppliedAtComparisonNotAtMeasurement(t *testing.T) {
	baseline, err := ParseBaseline("criterion/apply_movement/grounded | 100\n", Counted)
	if err != nil {
		t.Fatal(err)
	}
	within := map[string]int{"criterion/apply_movement/grounded": 104}
	if got := regressions(baseline, within, 5); len(got) != 0 {
		t.Errorf("4%% over a 5%% tolerance must pass: %+v", got)
	}
	over := map[string]int{"criterion/apply_movement/grounded": 106}
	if got := regressions(baseline, over, 5); len(got) != 1 {
		t.Errorf("6%% over a 5%% tolerance must fail: %+v", got)
	}
	if got := regressions(baseline, over, 0); len(got) != 1 {
		t.Errorf("no tolerance means an exact ceiling: %+v", got)
	}
}

func TestJSONCeilingLawIsSkippedEntirelyWhenDisarmed(t *testing.T) {
	root := criterionTree(t, "999999")
	writeLaw(t, root, "perf", `
name = "perf"
description = "a tier-1 kernel bench may not regress"
severity = "deny"
baseline = ".ratchet/baselines/perf.txt"

[scope]
include = ["**/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "criterion/**/new/estimates.json"
path = "mean.point_estimate"
enabled_env = "BORLD_PERF_RATCHET"
`)
	write(t, filepath.Join(root, ".ratchet", "baselines", "perf.txt"), "criterion/apply_movement/grounded | 50\n")

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a disarmed law makes no claim: %v", res.Lines())
	}
	if got := read(t, filepath.Join(root, ".ratchet", "baselines", "perf.txt")); got != "criterion/apply_movement/grounded | 50\n" {
		t.Errorf("a disarmed law must not tighten against data it never read: %q", got)
	}

	t.Setenv("BORLD_PERF_RATCHET", "1")
	res, err = Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("armed, the same tree regresses: %v", res.Lines())
	}
}

// `cargo metadata` is seconds on a big workspace and the gate runs before every
// commit, so the walk's verdict is cached against the inputs that can change
// it: Cargo.lock and every Cargo.toml.
func TestDepGraphCachesItsVerdictUntilAManifestMoves(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	write(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n")
	write(t, filepath.Join(root, metadataFixtureFile), metadataDoc)
	law := depGraphLaw(t, root)
	law.CacheDir = cache

	first, err := depGraphHits(root, law)
	if err != nil || len(first) != 1 {
		t.Fatalf("hits = %v (%v)", keys(first), err)
	}

	// The graph changes underneath, but no manifest moved: the cached verdict
	// stands, which is the whole point of the cache.
	write(t, filepath.Join(root, metadataFixtureFile), strings.Replace(metadataDoc,
		`{"pkg": "t 1", "dep_kinds": [{"kind": null}]}`,
		`{"pkg": "t 1", "dep_kinds": [{"kind": "dev"}]}`, 1))
	cached, err := depGraphHits(root, law)
	if err != nil || len(cached) != 1 {
		t.Fatalf("cached hits = %v (%v)", keys(cached), err)
	}

	write(t, filepath.Join(root, "Cargo.toml"), "[workspace]\nmembers = []\n")
	fresh, err := depGraphHits(root, law)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 0 {
		t.Fatalf("a moved manifest must re-walk: %v", keys(fresh))
	}
}

// targetGlobLaw judges numbers that land under `target/`, the one directory
// cargo lets an environment variable move.
func targetGlobLaw(t *testing.T, root string) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "perf"
description = "a budget nobody measures is a slogan"
severity = "deny"

[scope]
include = ["**/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "target/criterion/**/new/estimates.json"
path = "mean.point_estimate"
`, "perf")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

func TestJSONCeilingFollowsCargoTargetDirWithoutMovingTheBaselineKey(t *testing.T) {
	inTree := t.TempDir()
	write(t, filepath.Join(inTree, "target", "criterion", "apply_movement", "new", "estimates.json"),
		`{"mean": {"point_estimate": 46.3}}`)

	hits, err := jsonCeilingHits(inTree, targetGlobLaw(t, inTree), true, "")
	if err != nil {
		t.Fatalf("jsonCeilingHits: %v", err)
	}
	if len(hits) != 1 || hits[0].Key != "target/criterion/apply_movement" {
		t.Fatalf("without the env var the glob is rooted at the repo: %v", keys(hits))
	}

	// Same measurement, target dir moved elsewhere: the key must not move with it,
	// or every baseline entry is rewritten by an environment variable.
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "criterion", "apply_movement", "new", "estimates.json"),
		`{"mean": {"point_estimate": 46.3}}`)
	moved, err := jsonCeilingHits(t.TempDir(), targetGlobLaw(t, inTree), true, elsewhere)
	if err != nil {
		t.Fatalf("jsonCeilingHits: %v", err)
	}
	if len(moved) != 1 || moved[0].Key != hits[0].Key {
		t.Fatalf("keys = %v, want the same key as %v", keys(moved), keys(hits))
	}
	if moved[0].File != hits[0].File || moved[0].Weight != hits[0].Weight {
		t.Errorf("moved = %+v, want the same finding as %+v", moved[0], hits[0])
	}
}

func TestJSONCeilingIgnoresTheTargetDirForAGlobOutsideTarget(t *testing.T) {
	root := criterionTree(t, "46.3")
	hits, err := jsonCeilingHits(root, jsonCeilingLaw(t, root), true, t.TempDir())
	if err != nil {
		t.Fatalf("jsonCeilingHits: %v", err)
	}
	if len(hits) != 1 || hits[0].Key != "criterion/apply_movement/grounded" {
		t.Fatalf("a glob that is not under target/ stays rooted at the repo: %v", keys(hits))
	}
}
