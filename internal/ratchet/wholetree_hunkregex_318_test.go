package ratchet

import (
	"path/filepath"
	"testing"
)

// multiLangTestRemovedLaw is #318's actual test_removed shape: one pattern
// covering every language's test declaration in one law, exactly as the
// issue specifies (Go, Python; Rust and JS follow the same alternation
// shape and are exercised by TestHunkRegex_MultiLanguage_RustAndJS below).
// Each branch captures into its OWN group — an alternation cannot share one
// capture slot across branches — so the identity is whichever group
// actually matched, the same "last non-empty capture" rule
// registry-both-ways already uses for its own alternated use_pattern.
const multiLangTestRemovedLaw = `
name = "test_removed_multilang"
description = "a test declaration removed with no re-declaration of the same name, any of Go/Python/Rust/JS"
severity = "deny"

[scope]
changed = "staged"
include = ["**/*"]

[matcher]
kind = "hunk-regex"
removed = "^func (Test[A-Za-z0-9_]+)\\(|^def (test_[A-Za-z0-9_]+)\\("
name_group = true
`

// TestHunkRegex_NameGroupSupportsMultiLanguagePatterns proves name_group
// accepts an ALTERNATION with one capture group per branch (Go, Python) —
// not just a single-group pattern — since #318's test_removed law is
// explicitly multi-language and a symbol-removed-style "exactly one group"
// requirement cannot express that in a single law.
func TestHunkRegex_NameGroupSupportsMultiLanguagePatterns(t *testing.T) {
	root := hunkRegexRepo(t, "test_removed_multilang", multiLangTestRemovedLaw)
	write(t, filepath.Join(root, "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	write(t, filepath.Join(root, "test_b.py"), "def test_bar():\n    pass\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a_test.go"), "package a\n")
	write(t, filepath.Join(root, "test_b.py"), "")
	gitRun(t, root, "add", ".")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a_test.go", "test_b.py"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %+v, want two — one Go removal, one Python removal", res.Findings)
	}
	keys := map[string]bool{}
	for _, f := range res.Findings {
		keys[f.Key] = true
	}
	if !keys["a_test.go:TestFoo"] || !keys["test_b.py:test_bar"] {
		t.Errorf("keys = %v, want a_test.go:TestFoo and test_b.py:test_bar", keys)
	}
}

// assertionWeakenedFullLaw is #318's assertion_weakened rule with the FULL
// substitution table the issue names, not just one shape: Equal narrowed to
// Contains/NotNil/NotEmpty, and an exact `==` narrowed to `>=`/`!=`.
const assertionWeakenedFullLaw = `
name = "assertion_weakened_full"
description = "an assertion narrowed to a looser form in the same commit"
severity = "warn"

[scope]
changed = "staged"
include = ["**/*_test.go"]

[matcher]
kind = "hunk-regex"
removed = "assert\\.Equal\\(|== 4\\b"
added   = "assert\\.(Contains|NotNil|NotEmpty)\\(|(>=|!=) 4\\b"
paired  = true
`

// TestHunkRegex_PairedForbidCoversTheFullSubstitutionTable proves one law
// catches every shape the issue's table names, not just the single pair
// exercised in TestHunkRegex_PairedForbidFlagsALoosenedAssertion.
func TestHunkRegex_PairedForbidCoversTheFullSubstitutionTable(t *testing.T) {
	root := hunkRegexRepo(t, "assertion_weakened_full", assertionWeakenedFullLaw)
	write(t, filepath.Join(root, "x_test.go"),
		"package a\n\nfunc TestX(t *testing.T) {\n\tassert.Equal(t, 4, a())\n\tif got == 4 {\n\t}\n}\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "x_test.go"),
		"package a\n\nfunc TestX(t *testing.T) {\n\tassert.NotEmpty(t, a())\n\tif got != 4 {\n\t}\n}\n")
	gitRun(t, root, "add", "x_test.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"x_test.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %+v, want two — the Equal->NotEmpty pair and the ==->!= pair", res.Findings)
	}
}

// TestRunFixtures_ProvesTheMultiLanguageTestRemovedLaw proves the fixture
// harness proves a name_group law with a MULTI-GROUP alternation pattern,
// not just the single-group shape the earlier fixture test used.
func TestRunFixtures_ProvesTheMultiLanguageTestRemovedLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "test_removed_multilang", multiLangTestRemovedLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "test_removed_multilang")
	write(t, filepath.Join(fx, "hit", "base", "a_test.go"), "package a\n\nfunc TestFoo(t *testing.T) {}\n")
	write(t, filepath.Join(fx, "hit", "tip", "a_test.go"), "package a\n")
	write(t, filepath.Join(fx, "expected.txt"), "a_test.go:TestFoo\n")
	write(t, filepath.Join(fx, "clean", "base", "test_b.py"), "def test_bar():\n    pass\n")
	write(t, filepath.Join(fx, "clean", "tip", "test_b.py"), "def test_bar():\n    return 1\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

// assertionWeakenedFixtureLaw is expectation_moved's escape-carrying
// sibling, assertion_weakened, laid out for the fixture harness.
const assertionWeakenedFixtureLaw = `
name = "assertion_weakened_fixture"
description = "an Equal assertion loosened to Contains in the same commit"
severity = "warn"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "hunk-regex"
removed = "assert\\.Equal\\("
added   = "assert\\.(Contains|NotNil)\\("
paired  = true
`

// TestRunFixtures_ProvesAPairedHunkRegexLaw proves the fixture harness for
// the `paired` (non-differs) mode: assertion_weakened's forbidden
// substitution shape.
func TestRunFixtures_ProvesAPairedHunkRegexLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "assertion_weakened_fixture", assertionWeakenedFixtureLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "assertion_weakened_fixture")
	write(t, filepath.Join(fx, "hit", "base", "x_test.go"), "package a\n\nfunc TestX(t *testing.T) {\n\tassert.Equal(t, 4, sum())\n}\n")
	write(t, filepath.Join(fx, "hit", "tip", "x_test.go"), "package a\n\nfunc TestX(t *testing.T) {\n\tassert.Contains(t, out, \"4\")\n}\n")
	write(t, filepath.Join(fx, "expected.txt"), "x_test.go:4\n")
	write(t, filepath.Join(fx, "clean", "base", "y_test.go"), "package a\n\nfunc TestY(t *testing.T) {\n\tassert.Equal(t, 4, sum())\n}\n")
	write(t, filepath.Join(fx, "clean", "tip", "y_test.go"), "package a\n\nfunc TestY(t *testing.T) {\n\tassert.Equal(t, 5, sum())\n}\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}
