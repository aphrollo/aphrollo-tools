package ratchet

import (
	"path/filepath"
	"testing"
)

const anyWordLaw = `
name        = "any-word"
description = "a broad law with no exclude of its own"
severity    = "deny"

[scope]
include = ["**/*.ts"]

[matcher]
kind    = "regex-absent"
pattern = "BAD"
key     = "file:line-content-hash"
`

// TestCheck_NeverJudgesRatchetFixtures proves a law whose scope names no
// exclude still does not fire on a file under .ratchet/fixtures/: fixtures are
// the engine's test data, so a new law can never fail on its own hit fixture.
func TestCheck_NeverJudgesRatchetFixtures(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "any-word", anyWordLaw)
	write(t, filepath.Join(root, ".ratchet", "fixtures", "any-word", "hit", "src", "a.ts"), "BAD\n")
	write(t, filepath.Join(root, "src", "real.ts"), "fine\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a fixture file was judged: %+v", res.Findings)
	}
}

// TestScope_FixturesAreOutsideEveryScope proves the edit and commit law gates,
// which ask Scope.Matches directly, see fixtures as out of scope too, while a
// path that merely contains the words stays in.
func TestScope_FixturesAreOutsideEveryScope(t *testing.T) {
	s := Scope{Include: []string{"**/*.ts"}}
	if s.Matches(".ratchet/fixtures/law/hit/a.ts") {
		t.Error("a fixture file matched a scope")
	}
	if s.couldMatchUnder(".ratchet/fixtures") {
		t.Error("the walk would descend into the fixtures dir")
	}
	if !s.Matches("src/.ratchet/fixtures/a.ts") {
		t.Error("a nested path that only resembles the fixtures dir was excluded")
	}
}
