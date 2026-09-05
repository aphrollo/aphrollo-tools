package ratchet

import "testing"

const stagedScopeLaw = `
name = "twin-diff"
description = "proves scope.changed parses"
severity = "warn"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`

// TestLoadLaws_ParsesScopeChangedStaged proves `[scope] changed = "staged"`
// reaches Scope.Changed rather than being silently dropped as an unknown key
// — the diff-relational engine's whole INPUT selection rides on this field.
func TestLoadLaws_ParsesScopeChangedStaged(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "twin-diff", stagedScopeLaw)

	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 1 {
		t.Fatalf("loaded %d laws, want 1", len(laws))
	}
	if laws[0].Scope.Changed != ChangedStaged {
		t.Fatalf("Scope.Changed = %q, want %q", laws[0].Scope.Changed, ChangedStaged)
	}
}

// TestLoadLaws_RejectsAnUnknownChangedValue proves a typo in scope.changed is
// a load-time error, not a silently-disarmed law — the same strictness every
// other enum-shaped key in this engine carries.
func TestLoadLaws_RejectsAnUnknownChangedValue(t *testing.T) {
	dir := t.TempDir()
	body := `
name = "twin-diff"
description = "x"
severity = "warn"

[scope]
changed = "worktree"
include = ["**/*.go"]

[matcher]
kind = "co-change"
`
	writeLaw(t, dir, "twin-diff", body)

	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("LoadLaws did not reject an unknown scope.changed value")
	}
}
