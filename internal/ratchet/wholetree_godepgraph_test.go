package ratchet

import (
	"path/filepath"
	"testing"
)

// goListFixtureJSON is `go list -json`'s own shape: concatenated JSON
// objects with no enclosing array and no commas between them — exactly what
// a checked-in copy of the real command's output would look like, unedited.
const goListFixtureJSON = `{"ImportPath":"example.com/app/a","Imports":["example.com/app/b","fmt"],"Standard":false}
{"ImportPath":"example.com/app/b","Imports":["example.com/app/c","strings"],"Standard":false}
{"ImportPath":"example.com/app/c","Imports":[],"Standard":false}
{"ImportPath":"fmt","Imports":[],"Standard":true}
{"ImportPath":"strings","Imports":[],"Standard":true}
`

const goDepGraphLaw = `
name = "no_reach_c"
description = "internal/ratchet never reaches internal/tdd — Go's layering law"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = ["example.com/app/a"]
forbidden = ["example.com/app/c"]
`

func goDepGraphRepo(t *testing.T, lawBody string) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "no_reach_c", lawBody)
	write(t, filepath.Join(root, goListFixtureFile), goListFixtureJSON)
	return root
}

// TestGoDepGraphForbids_ReportsATransitiveReachAsAPath proves the walk is
// transitive (a -> b -> c, not just a's own direct imports) and that the
// decoder reads `go list -json`'s concatenated-object stream, not an array.
func TestGoDepGraphForbids_ReportsATransitiveReachAsAPath(t *testing.T) {
	root := goDepGraphRepo(t, goDepGraphLaw)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	if res.Findings[0].Key != "example.com/app/a->example.com/app/b->example.com/app/c" {
		t.Errorf("key = %q, want the full reach path", res.Findings[0].Key)
	}
}

// TestGoDepGraphForbids_SilentWhenTheForbiddenPackageIsUnreached proves the
// law discriminates: pointing forbidden at a package the root cannot reach
// through the graph produces no hit.
func TestGoDepGraphForbids_SilentWhenTheForbiddenPackageIsUnreached(t *testing.T) {
	root := goDepGraphRepo(t, `
name = "no_reach_c"
description = "x"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = ["example.com/app/b"]
forbidden = ["example.com/app/nonexistent"]
`)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none", res.Findings)
	}
}

// TestGoDepGraphForbids_WildcardRootsWalksEveryModulePackage proves
// `roots = "*"` resolves through go.mod's module line to every package this
// module owns, standard-library packages excluded.
func TestGoDepGraphForbids_WildcardRootsWalksEveryModulePackage(t *testing.T) {
	root := goDepGraphRepo(t, `
name = "no_reach_c"
description = "x"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = "*"
forbidden = ["example.com/app/c"]
`)
	write(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.22\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %+v, want two — both a and b reach c", res.Findings)
	}
}

// TestRunFixtures_ProvesAGoDepGraphLaw proves the fixture harness itself:
// `ratchet test` judges a go-dep-graph-forbids law's `hit`/`clean` cases
// through its own checked-in go-list.json, exactly like the cargo kind's
// cargo-metadata.json fixtures.
func TestRunFixtures_ProvesAGoDepGraphLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no_reach_c", goDepGraphLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "no_reach_c")
	write(t, filepath.Join(fx, "hit", goListFixtureFile), goListFixtureJSON)
	write(t, filepath.Join(fx, "expected.txt"), "example.com/app/a->example.com/app/b->example.com/app/c\n")
	write(t, filepath.Join(fx, "clean", goListFixtureFile), `{"ImportPath":"example.com/app/a","Imports":["example.com/app/b"],"Standard":false}
{"ImportPath":"example.com/app/b","Imports":[],"Standard":false}
`)

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

// TestGoDepGraphForbids_RefusesAVacuousWalk proves the same vacuity refusal
// the cargo kind has: a NAMED root that resolves nothing at all is a broken
// walk, not a clean verdict.
func TestGoDepGraphForbids_RefusesAVacuousWalk(t *testing.T) {
	root := goDepGraphRepo(t, `
name = "no_reach_c"
description = "x"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = ["example.com/app/c"]
forbidden = ["example.com/app/nothing"]
`)
	_, err := Check(Options{Root: root})
	if err == nil {
		t.Fatal("Check did not refuse a walk that reached nothing")
	}
}
