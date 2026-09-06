package ratchet

import (
	"path/filepath"
	"testing"
)

// #324's third check: a backticked identifier or config claim cited in a Go
// comment or a *.md file must resolve against a real declaration. No real
// `.ratchet/laws/*.toml` lands for this here: this repo's own commit gate
// judges its laws through the globally installed aphrollo binary, which
// predates `ident-resolves`, and declaring one would reject every commit
// until that binary is rebuilt on merge — the same reason #315/#318's and
// #492's laws stayed fixture-only (see wholetree_hunkregex_318_test.go and
// wholetree_containment_492_test.go).
//
// The pattern is spliced around two literal backticks — the citation
// delimiters — because a Go raw string cannot contain one; the same
// splice wholetree_containment_492_test.go already uses for its own
// backtick-carrying capture pattern.
const identResolvesFixtureLaw = `
name = "ident_resolves_fixture"
description = "a backticked identifier or a config claim cited in a Go comment or *.md file must resolve against a real declaration or TOML key"
severity = "deny"
escape = "// ident-ok:"

[scope]
include = ["**/*.md", "**/*.go"]

[matcher]
kind = "ident-resolves"
pattern = "` + "`" + `([A-Z][A-Za-z0-9]*|[a-z][a-z0-9_]*\\(|[a-z][a-zA-Z0-9_]*\\.[A-Za-z][A-Za-z0-9_]*|[a-z][a-z0-9_-]*\\s*=\\s*.+?|[a-z][a-z0-9]*(?:[_-][a-z0-9]+)+)` + "`" + `"
`

// TestRunFixtures_ProvesIdentResolvesCatchesAStaleConfigClaim proves the
// #191 shape exactly: a *.md line claims a backticked `key = value`, and the
// tracked TOML file sets that key to a DIFFERENT value — the pipeline.yml
// comment claiming `mutants-local = false` while aphrollo.toml (and a
// pinning test) say `true`. It also proves the plain-identifier half of the
// same law in one pass: a *.md citation of a Go symbol nothing declares.
func TestRunFixtures_ProvesIdentResolvesCatchesAStaleConfigClaim(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "ident_resolves_fixture", identResolvesFixtureLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "ident_resolves_fixture")

	write(t, filepath.Join(fx, "hit", "aphrollo.toml"), "mutants-local = true\n")
	write(t, filepath.Join(fx, "hit", "decl.go"), "package sample\n\n// See RunWidget for the entrypoint.\nfunc RunWidget() {}\n")
	write(t, filepath.Join(fx, "hit", "report.md"),
		"# Report\n\nThe job comment claims `mutants-local = false`, but see below.\nIt also references `RunGadget`, which does not exist.\n")
	write(t, filepath.Join(fx, "expected.txt"), "report.md:3\nreport.md:4\n")

	write(t, filepath.Join(fx, "clean", "config.toml"), "mutants-local = true\n")
	write(t, filepath.Join(fx, "clean", "decl.go"), "package sample\n\n// See RunWidget for the entrypoint.\nfunc RunWidget() {}\n")
	write(t, filepath.Join(fx, "clean", "notes.md"),
		"# Notes\n\nThe job comment claims `mutants-local = true`, matching config.\nIt also references `RunWidget`, which is real.\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

// TestRunFixtures_ProvesIdentResolvesGoCommentAndEscape proves the Go-file
// half separately: a stale identifier cited from a `//` comment (never from
// code — a citation inside a string literal or a call must never count) is
// a hit, and the same citation with `// ident-ok:` on the line is not.
func TestRunFixtures_ProvesIdentResolvesGoCommentAndEscape(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "ident_resolves_fixture", identResolvesFixtureLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "ident_resolves_fixture")

	write(t, filepath.Join(fx, "hit", "caller.go"),
		"package sample\n\n// TODO: replace with `MissingHelper` once it lands.\nfunc call() {}\n")
	write(t, filepath.Join(fx, "expected.txt"), "caller.go:3\n")

	write(t, filepath.Join(fx, "clean", "helper.go"), "package sample\n\nfunc Helper() {}\n")
	write(t, filepath.Join(fx, "clean", "caller.go"),
		"package sample\n\n// Uses `Helper`; `MissingHelper` is fine here. // ident-ok: renamed, tracked in #1\nfunc call() { Helper() }\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

// TestDeclIndex_Resolve is a direct unit test of the resolver's four shapes,
// isolated from the fixture harness: a matching key=value claim, a
// mismatched one, a resolving Go identifier and a package-qualified one, and
// an unresolved bare identifier. Mutation proof: flipping resolve's
// `actual != claimed` to `==` fails this test on both key=value cases
// (`mutants-local = true` reads as unresolved, `mutants-local = false`
// reads as resolved) — restored byte-identically.
func TestDeclIndex_Resolve(t *testing.T) {
	idx := declIndex{
		names: map[string]bool{"RunWidget": true, "run_task": true},
		toml:  map[string]string{"mutants-local": "true"},
	}
	cases := []struct {
		cited string
		want  bool
	}{
		{"mutants-local = true", true},
		{"mutants-local = false", false},
		{"RunWidget", true},
		{"run_task(", true},
		{"pkg.RunWidget", true},
		{"NeverDeclared", false},
		{"never-declared-key", false},
	}
	for _, c := range cases {
		ok, what := idx.resolve(c.cited)
		if ok != c.want {
			t.Errorf("resolve(%q) = (%v, %q), want ok=%v", c.cited, ok, what, c.want)
		}
	}
}
