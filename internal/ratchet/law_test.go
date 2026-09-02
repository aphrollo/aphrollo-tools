package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLaw(t *testing.T, dir, name, body string) string {
	t.Helper()
	lawDir := filepath.Join(dir, ".ratchet", "laws")
	if err := os.MkdirAll(lawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lawDir, name+".toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const nanGuardLaw = `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
escape = "// nan-safe:"
escape_lines = 2
baseline = ".ratchet/baselines/nan-guard.txt"
code_only = true

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
key = "file:line-content-hash"
`

func TestLoadLawsReadsEveryFieldOfALaw(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "nan-guard", nanGuardLaw)

	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 1 {
		t.Fatalf("loaded %d laws, want 1", len(laws))
	}
	l := laws[0]
	if l.Name != "nan-guard" || l.Severity != Deny || l.Escape != "// nan-safe:" || l.EscapeLines != 2 {
		t.Errorf("law header = %+v", l)
	}
	if !l.CodeOnly || l.Baseline != ".ratchet/baselines/nan-guard.txt" {
		t.Errorf("law options = %+v", l)
	}
	if l.Matcher.Kind != KindRegexAbsent || l.Matcher.Key != KeyLineContent {
		t.Errorf("matcher = %+v", l.Matcher)
	}
	if !l.Matcher.Pattern.MatchString("x.clamp(0.0, 1.0)") {
		t.Errorf("pattern did not compile to the intended regex")
	}
	if !l.Scope.Matches("crates/pose/src/advance.rs") {
		t.Error("include glob must match a crate source file")
	}
	if l.Scope.Matches("crates/pose/target/debug/x.rs") {
		t.Error("exclude glob must win over include")
	}
}

func TestLoadLawsDefaultsEscapeLinesAndKey(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "no-todo", `
name = "no-todo"
description = "no TODO markers"
severity = "warn"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if laws[0].EscapeLines != 2 {
		t.Errorf("escape_lines default = %d, want 2", laws[0].EscapeLines)
	}
	if laws[0].Matcher.Key != KeyLineContent {
		t.Errorf("default key = %v, want content identity", laws[0].Matcher.Key)
	}
	if laws[0].Severity != Warn {
		t.Errorf("severity = %v", laws[0].Severity)
	}
}

func TestLoadLawsRejectsBadSchema(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"unknown root key": {`
name = "x"
description = "d"
severity = "deny"
sevrity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`, "sevrity"},
		"unknown matcher key": {`
name = "x"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "line-count"
max = 600
pattern = "x"
`, "pattern"},
		"unknown table": {`
name = "x"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
[extra]
a = "b"
`, "extra"},
		"bad severity": {`
name = "x"
description = "d"
severity = "loud"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`, "severity"},
		"unknown matcher kind": {`
name = "x"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "vibes"
`, "vibes"},
		"missing matcher key": {`
name = "x"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "marker-within-lines"
trigger = "Vec<"
`, "marker"},
		"empty include": {`
name = "x"
description = "d"
severity = "deny"
[scope]
include = []
[matcher]
kind = "regex-absent"
pattern = "x"
`, "include"},
		"no description": {`
name = "x"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`, "description"},
		"bad regex": {`
name = "x"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "("
`, "pattern"},
		"name disagrees with filename": {`
name = "other"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`, "other"},
	}
	for label, tc := range cases {
		dir := t.TempDir()
		writeLaw(t, dir, "x", tc.body)
		_, err := LoadLaws(dir)
		if err == nil {
			t.Errorf("%s: loaded without error", label)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name %q", label, err, tc.want)
		}
	}
}

func TestLoadLawsOnARepoWithoutLawsIsEmptyNotAnError(t *testing.T) {
	laws, err := LoadLaws(t.TempDir())
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 0 {
		t.Errorf("loaded %d laws from a repo with none", len(laws))
	}
}

func TestLoadLawsSortsByNameSoOutputIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		writeLaw(t, dir, n, `
name = "`+n+`"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`)
	}
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	got := []string{laws[0].Name, laws[1].Name, laws[2].Name}
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestLoadLawsReadsPathRegexAbsentAndTheGitignoreOptOut(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "doc-names", `
name = "doc-names"
description = "a name says what a thing is, not when it was written"
severity = "deny"

[scope]
include = ["**/*.md"]
ignore_gitignore = true

[matcher]
kind = "path-regex-absent"
pattern = "task\d+"
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 1 {
		t.Fatalf("loaded %d laws, want 1", len(laws))
	}
	l := laws[0]
	if !l.Scope.IgnoreGitignore {
		t.Errorf("scope.ignore_gitignore did not reach the law: %+v", l.Scope)
	}
	if l.Matcher.Kind != KindPathRegexAbsent || l.Matcher.Key != KeyFile {
		t.Errorf("matcher = %+v", l.Matcher)
	}
	if l.Matcher.Pattern == nil || !l.Matcher.Pattern.MatchString("task19_probe.md") {
		t.Errorf("pattern = %+v", l.Matcher.Pattern)
	}
}

func TestLoadLawsRejectsANonBooleanGitignoreOptOutAndAKeyOnAPathLaw(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "bad", `
name = "bad"
description = "d"
severity = "deny"

[scope]
include = ["**/*.md"]
ignore_gitignore = ["yes"]

[matcher]
kind = "path-regex-absent"
pattern = "x"
`)
	if _, err := LoadLaws(dir); err == nil || !strings.Contains(err.Error(), "ignore_gitignore") {
		t.Fatalf("err = %v, want one naming ignore_gitignore", err)
	}

	dir2 := t.TempDir()
	writeLaw(t, dir2, "bad2", `
name = "bad2"
description = "d"
severity = "deny"

[scope]
include = ["**/*.md"]

[matcher]
kind = "path-regex-absent"
pattern = "x"
key = "file"
`)
	if _, err := LoadLaws(dir2); err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("err = %v, want one naming the unsupported key", err)
	}
}
