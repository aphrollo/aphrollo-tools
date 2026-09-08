package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// #589's two halves, which together are what made a real dep-graph-ceiling law
// unadoptable: no `clean/` fixture could exist for the kind (every root emits a
// hit, weight 0 included, and runLawFixtures failed the fixture on any hit at
// all), and the `escape` field the kind's own issue promised was honoured only
// by file-set-containment.

// crateFanoutLaw is a dep-graph-ceiling law with an escape token, written the
// way a repo writes one so LoadLaws reads it through its ordinary path.
const crateFanoutLaw = `name = "crate-fanout"
description = "a root may not reach more of the workspace than its baseline"
severity = "deny"
escape = "crate-fanout-ok:"
baseline = ".ratchet/baselines/crate-fanout.txt"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
roots = ["leaf"]
`

// leafReachesAWorkspaceMember: leaf -> helper, both members, so the ceiling
// has a real number to say about leaf (weight 1).
const leafReachesAWorkspaceMember = `{
  "packages": [{"id": "l 1", "name": "leaf"}, {"id": "h 1", "name": "helper"}],
  "workspace_members": ["l 1", "h 1"],
  "resolve": {"nodes": [
    {"id": "l 1", "deps": [{"pkg": "h 1", "dep_kinds": [{"kind": null}]}]},
    {"id": "h 1", "deps": []}
  ]}
}`

// leafReachesOnlyOutside: leaf -> serde, which is NOT a workspace member. The
// walk resolved something, so it is not vacuous and raises no error, and the
// COUNT is zero. That is the only shape a clean case for this kind can take —
// the three alternatives #589 enumerates all close — so if a weight-0 hit
// fails a clean fixture, no repo can carry a dep-graph-ceiling law past
// `ratchet test`, which the commit gate runs.
const leafReachesOnlyOutside = `{
  "packages": [{"id": "l 1", "name": "leaf"}, {"id": "sd 1", "name": "serde"}],
  "workspace_members": ["l 1"],
  "resolve": {"nodes": [
    {"id": "l 1", "deps": [{"pkg": "sd 1", "dep_kinds": [{"kind": null}]}]},
    {"id": "sd 1", "deps": []}
  ]}
}`

const leafManifest = "[package]\nname = \"leaf\"\n"

// crateFanoutFixtureRepo lays out the law and its `hit/`+`clean/` fixtures,
// with the clean side's resolved graph given by the caller.
func crateFanoutFixtureRepo(t *testing.T, cleanMetadata string) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "crate-fanout", crateFanoutLaw)
	dir := filepath.Join(root, ".ratchet", "fixtures", "crate-fanout")
	write(t, filepath.Join(dir, "hit", metadataFixtureFile), leafReachesAWorkspaceMember)
	write(t, filepath.Join(dir, "hit", "leaf", "Cargo.toml"), leafManifest)
	write(t, filepath.Join(dir, "expected.txt"), "leaf\n")
	write(t, filepath.Join(dir, "clean", metadataFixtureFile), cleanMetadata)
	write(t, filepath.Join(dir, "clean", "leaf", "Cargo.toml"), leafManifest)
	return root
}

// crateFanoutLawFor parses crateFanoutLaw for a direct matcher call.
func crateFanoutLawFor(t *testing.T, root string) Law {
	t.Helper()
	law, err := ParseLaw(crateFanoutLaw, "crate-fanout")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

func TestFixtures_DepGraphCeilingCleanFixtureWithZeroWeightHitsPasses(t *testing.T) {
	results, err := RunFixtures(crateFanoutFixtureRepo(t, leafReachesOnlyOutside))

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one per law", results)
	}
	if len(results[0].Failures) != 0 {
		t.Fatalf("a clean fixture whose every hit weighs 0 is judged against a zero baseline and must pass: %v",
			results[0].Failures)
	}
}

func TestFixtures_DepGraphCeilingCleanFixtureWithAReachingRootFails(t *testing.T) {
	results, err := RunFixtures(crateFanoutFixtureRepo(t, leafReachesAWorkspaceMember))

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one per law", results)
	}
	joined := strings.Join(results[0].Failures, "\n")
	if !strings.Contains(joined, "leaf reaches 1 package") {
		t.Fatalf("a clean fixture whose root reaches a workspace package must still fail, naming what it reached: %q",
			joined)
	}
}

func TestDepGraphCeiling_EscapeCommentWithReasonExemptsTheRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), leafReachesAWorkspaceMember)
	write(t, filepath.Join(root, "leaf", "Cargo.toml"),
		"# crate-fanout-ok: helper is the shared type crate every member depends on, by design\n"+leafManifest)

	hits, err := depGraphCeilingHits(root, crateFanoutLawFor(t, root))

	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("a root whose Cargo.toml carries the escape with a reason is exempt from the ceiling for that "+
			"run, so the only way to admit a deliberate edge is not a raised baseline the guard refuses: %+v", hits)
	}
}

func TestDepGraphCeiling_EscapeWithoutReasonIsNotAnEscape(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, metadataFixtureFile), leafReachesAWorkspaceMember)
	write(t, filepath.Join(root, "leaf", "Cargo.toml"), "# crate-fanout-ok:\n"+leafManifest)

	hits, err := depGraphCeilingHits(root, crateFanoutLawFor(t, root))

	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Weight != 1 {
		t.Fatalf("presence of the token with no reviewed reason after it is not an escape: %+v", hits)
	}
}
