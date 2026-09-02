package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "hit", "bare.rs"),
		"let a = 1;\nlet b = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "expected.txt"),
		"# the shape the law is looking for\nbare.rs:2\n")
	write(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "clean", "guarded.rs"),
		"let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\nlet b = y.clamp(0.0, 1.0); // nan-safe: literal bounds\n")
	return root
}

func TestRunFixturesPassesWhenHitsMatchExpectedAndCleanIsSilent(t *testing.T) {
	results, err := RunFixtures(fixtureRepo(t))
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	if len(results[0].Failures) != 0 {
		t.Fatalf("failures = %v", results[0].Failures)
	}
	if results[0].HitFiles != 1 || results[0].CleanFiles != 1 {
		t.Errorf("counts = %+v", results[0])
	}
}

func TestRunFixturesFailsWhenAHitFixtureStopsHitting(t *testing.T) {
	root := fixtureRepo(t)
	// A scanner that silently stopped matching would report a clean tree
	// forever; the hit fixture is what notices.
	write(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "hit", "bare.rs"), "let a = 1;\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Failures) != 1 || !strings.Contains(results[0].Failures[0], "bare.rs:2") {
		t.Fatalf("failures = %v", results[0].Failures)
	}
}

func TestRunFixturesFailsOnAnUnexpectedHitAndOnADirtyCleanFixture(t *testing.T) {
	root := fixtureRepo(t)
	write(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "hit", "bare.rs"),
		"let a = w.clamp(0.0, 1.0);\nlet b = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "clean", "guarded.rs"),
		"let a = z.clamp(0.0, 1.0);\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(results[0].Failures, "\n")
	if !strings.Contains(joined, "bare.rs:1") {
		t.Errorf("an unexpected hit must be named:\n%s", joined)
	}
	if !strings.Contains(joined, "guarded.rs:1") {
		t.Errorf("a clean fixture that hits must be named:\n%s", joined)
	}
}

// A law nobody proved catches nothing: an unfixtured law is a failure, not a
// silent pass.
func TestRunFixturesFailsALawWithNoFixtures(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	results, err := RunFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Failures) == 0 {
		t.Fatalf("results = %+v", results)
	}
	if !strings.Contains(strings.Join(results[0].Failures, " "), ".ratchet/fixtures/nan-guard") {
		t.Errorf("the failure must name where the fixtures belong: %v", results[0].Failures)
	}
}

func TestRunFixturesFailsWhenOnlyOneDirectionIsProved(t *testing.T) {
	root := fixtureRepo(t)
	if err := os.RemoveAll(filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "clean")); err != nil {
		t.Fatal(err)
	}
	results, err := RunFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Failures) == 0 {
		t.Fatal("a law with no clean fixture proves only half of itself")
	}
}

// The registry matcher judges the whole scope at once, so its fixture is a
// small tree: a registry file plus the sources that use it.
func TestRunFixturesCoversARegistryLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "env-registry", `
name = "env-registry"
description = "every env switch is registered"
severity = "deny"

[scope]
include = ["**/*.rs"]

[matcher]
kind = "registry-both-ways"
registry_file = "registry.txt"
entry_pattern = "^([A-Z][A-Z0-9_]+) \\|"
use_pattern = "env::var\\(\"([A-Z][A-Z0-9_]+)\"\\)"
`)
	base := filepath.Join(root, ".ratchet", "fixtures", "env-registry")
	write(t, filepath.Join(base, "hit", "registry.txt"), "BORLD_KNOWN | a | b\n")
	write(t, filepath.Join(base, "hit", "src.rs"), "env::var(\"BORLD_NEW\")\nenv::var(\"BORLD_KNOWN\")\n")
	write(t, filepath.Join(base, "expected.txt"), "src.rs:1\n")
	write(t, filepath.Join(base, "clean", "registry.txt"), "BORLD_KNOWN | a | b\n")
	write(t, filepath.Join(base, "clean", "src.rs"), "env::var(\"BORLD_KNOWN\")\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results[0].Failures) != 0 {
		t.Fatalf("failures = %v", results[0].Failures)
	}
}

// A whole-tree law's fixture is a small world of its own: a checked-in
// resolved graph, the two files a containment law compares, or a directory of
// generated JSON. Its expectations are the hits' keys, since none of them
// points at a line.
func TestRunFixturesCoversTheWholeTreeLaws(t *testing.T) {
	root := t.TempDir()

	writeLaw(t, root, "prod-graph", `
name = "prod-graph"
description = "dev-only tooling never reaches a shipping binary"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-forbids"
roots = ["server"]
forbidden = ["testrig"]
`)
	graph := filepath.Join(root, ".ratchet", "fixtures", "prod-graph")
	write(t, filepath.Join(graph, "hit", metadataFixtureFile), metadataDoc)
	write(t, filepath.Join(graph, "expected.txt"), "server->shared->testrig\n")
	write(t, filepath.Join(graph, "clean", metadataFixtureFile),
		strings.Replace(metadataDoc, `{"pkg": "t 1", "dep_kinds": [{"kind": null}]}`,
			`{"pkg": "t 1", "dep_kinds": [{"kind": "dev"}]}`, 1))

	writeLaw(t, root, "perf", `
name = "perf"
description = "a tier-1 kernel bench may not regress"
severity = "deny"

[scope]
include = ["**/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "criterion/**/new/estimates.json"
path = "mean.point_estimate"
`)
	perf := filepath.Join(root, ".ratchet", "fixtures", "perf")
	write(t, filepath.Join(perf, "hit", "criterion", "kernel", "case", "new", "estimates.json"),
		`{"mean": {"point_estimate": 46.3}}`)
	write(t, filepath.Join(perf, "expected.txt"), "criterion/kernel/case\n")
	// A measurement law's clean case is a file it must NOT read: criterion's
	// `base/` is the previous run's copy, and reading it would report last
	// week's number as today's.
	write(t, filepath.Join(perf, "clean", "criterion", "kernel", "case", "base", "estimates.json"),
		`{"mean": {"point_estimate": 46.3}}`)

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	for _, r := range results {
		if len(r.Failures) != 0 {
			t.Errorf("%s: %v", r.Law, r.Failures)
		}
	}
}
