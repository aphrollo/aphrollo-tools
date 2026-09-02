package ratchet

import "testing"

func TestMatchGlobHandlesTheShapesLawScopesUse(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"crates/**/*.rs", "crates/pose/src/advance.rs", true},
		{"crates/**/*.rs", "crates/pose.rs", true},
		{"crates/**/*.rs", "tools/pose/src/advance.rs", false},
		{"crates/**/*.rs", "crates/pose/src/advance.ron", false},
		{"**/target/**", "crates/pose/target/debug/x.rs", true},
		{"**/target/**", "target/debug/x.rs", true},
		{"**/target/**", "crates/pose/src/target.rs", false},
		{"*.go", "main.go", true},
		{"*.go", "internal/cli/main.go", false},
		{"**/*.go", "internal/cli/main.go", true},
		{"crates/ratchet/tests/**", "crates/ratchet/tests/a/b.rs", true},
		{"crates/ratchet/tests/**", "crates/ratchet/testsuite.rs", false},
		{"docs/?.md", "docs/a.md", true},
		{"docs/?.md", "docs/ab.md", false},
		{"crates/*/src/lib.rs", "crates/pose/src/lib.rs", true},
		{"crates/*/src/lib.rs", "crates/pose/deep/src/lib.rs", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestScopeExcludeWinsOverInclude(t *testing.T) {
	s := Scope{Include: []string{"crates/**/*.rs"}, Exclude: []string{"**/generated/**"}}
	if !s.Matches("crates/a/src/x.rs") {
		t.Error("plain include must match")
	}
	if s.Matches("crates/a/generated/x.rs") {
		t.Error("exclude must win")
	}
	if s.Matches("crates/a/src/x.go") {
		t.Error("a non-included path must not match")
	}
}

func TestScopeMatchesNormalizesWindowsSeparators(t *testing.T) {
	s := Scope{Include: []string{"crates/**/*.rs"}}
	if !s.Matches(`crates\pose\src\advance.rs`) {
		t.Error("a backslash path must match the same glob as its slash form")
	}
}

// A directory can be pruned only when NO include pattern could still match
// something below it — pruning `crates/` because it isn't itself a match would
// hide the whole tree.
func TestScopeCouldMatchUnderPrunesOnlyHopelessDirectories(t *testing.T) {
	s := Scope{Include: []string{"crates/**/*.rs"}, Exclude: []string{"**/target/**"}}
	if !s.couldMatchUnder("crates") {
		t.Error("crates/ must be walked")
	}
	if !s.couldMatchUnder("crates/pose/src") {
		t.Error("a deep dir under the include root must be walked")
	}
	if s.couldMatchUnder("tools") {
		t.Error("a dir no include pattern can reach must be pruned")
	}
	if s.couldMatchUnder("crates/pose/target") {
		t.Error("an excluded dir must be pruned")
	}
}
