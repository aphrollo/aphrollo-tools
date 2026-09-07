package ratchet

import (
	"errors"
	"strings"
	"testing"
)

// TestLawMatcherFields_SymbolRemovedNeedsOneCaptureGroup proves a
// symbol-removed law's `pattern` is rejected at load when it does not
// capture exactly one group — the group is the symbol name the whole-tree
// walk keys its findings by, and a pattern with none (or several) leaves
// that identity undefined.
func TestLawMatcherFields_SymbolRemovedNeedsOneCaptureGroup(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "test_removed", `
name = "test_removed"
description = "A test's disappearance from a diff needs a tombstone, not silence"
severity = "deny"

[scope]
include = ["**/*_test.go"]

[matcher]
kind = "symbol-removed"
pattern = "^func Test[A-Za-z0-9_]+\\("
`)
	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("err = nil, want a validation error naming the law and the missing capture group")
	}
	if !strings.Contains(err.Error(), "test_removed") {
		t.Errorf("err = %q, want it to name the law", err)
	}
	if !strings.Contains(err.Error(), "capture group") {
		t.Errorf("err = %q, want it to name the missing capture group", err)
	}
}

// TestParseLaw_MissingMatcherKindIsRejectedNotSkipped is #478: a [matcher]
// table with no kind key at all used to fall through the same path as a
// kind this binary predates, returning a law with an empty Matcher.Kind and
// a nil error — a law that loads clean and matches nothing. Missing a kind
// entirely is a malformed file, not a forward-compat skip, and must be
// rejected at parse time naming the law's file.
func TestParseLaw_MissingMatcherKindIsRejectedNotSkipped(t *testing.T) {
	dir := t.TempDir()
	path := writeLaw(t, dir, "no_kind", `
name = "no_kind"
description = "0"
severity = "warn"
[scope]
include = [""]
[matcher]
`)
	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("LoadLaws err = nil, want a rejection naming the missing matcher.kind")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %q, want it to name the law file %q", err, path)
	}
	if !strings.Contains(err.Error(), "matcher.kind") {
		t.Errorf("err = %q, want it to name matcher.kind as the missing field", err)
	}
	var unknown *UnknownMatcherKindError
	if errors.As(err, &unknown) {
		t.Fatalf("err wraps UnknownMatcherKindError{%q} — a missing kind must be a hard rejection, never the forward-compat skip a future kind gets", unknown.Kind)
	}
}

// TestParseLaw_EmptyMatcherKindIsRejected is the sibling case: kind = ""
// stated explicitly must fail the same way a kind key that was never
// written does, not compare equal to some matcher kind's empty zero value.
func TestParseLaw_EmptyMatcherKindIsRejected(t *testing.T) {
	_, err := ParseLaw(`
name = "x"
description = "d"
severity = "warn"
[scope]
include = ["**/*.go"]
[matcher]
kind = ""
`, "x")
	if err == nil {
		t.Fatal("err = nil, want a rejection — kind = \"\" names no rule")
	}
	if !strings.Contains(err.Error(), "matcher.kind") {
		t.Errorf("err = %q, want it to name matcher.kind", err)
	}
}

// TestParseLaw_NumericLookingMatcherKindIsRejected is #538: FuzzParseLaw found
// that `kind = "0"` parsed clean with a nil error and a zero-value
// Matcher.Kind — ParseLaw itself swallowed the forward-compat
// UnknownMatcherKindError instead of leaving that stand-down to LoadLaws (the
// caller UnknownMatcherKindError's own doc comment says handles it), so a
// direct ParseLaw call returned "loaded fine, matches nothing" for any kind
// this binary does not recognize, numeric-looking or not.
func TestParseLaw_NumericLookingMatcherKindIsRejected(t *testing.T) {
	_, err := ParseLaw(`name="x"
description="0"
severity="warn"
[scope]
include=[""]
[matcher]
kind="0"
`, "x")
	if err == nil {
		t.Fatal("err = nil, want a rejection — a direct ParseLaw call must never return nil error with an empty Matcher.Kind")
	}
	var unknown *UnknownMatcherKindError
	if !errors.As(err, &unknown) || unknown.Kind != "0" {
		t.Errorf("err = %v, want an UnknownMatcherKindError{Kind: %q}", err, "0")
	}
}

// TestLoadLaws_UnknownStringMatcherKindStillSkips proves the fix does not
// regress #440's forward-compat path: a kind that IS a non-empty string but
// simply is not one this binary's matcherKeys table knows must still load
// as UnknownKind rather than fail the whole tree.
func TestLoadLaws_UnknownStringMatcherKindStillSkips(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "future", `
name = "future"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "vibes"
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 1 || laws[0].UnknownKind != "vibes" {
		t.Fatalf("laws = %+v, want one law with UnknownKind = %q", laws, "vibes")
	}
}

// TestLoadLaws_DepGraphCeilingDefaultsCountsToWorkspace proves a
// dep-graph-ceiling law that never states matcher.counts gets the safe
// default: third-party fan-out never silently inflates the ceiling.
func TestLoadLaws_DepGraphCeilingDefaultsCountsToWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "crate-fanout", `
name = "crate-fanout"
description = "a root may not reach more of the workspace than its baseline"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
roots = ["account"]
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if laws[0].Matcher.Counts != "workspace" {
		t.Errorf("Counts = %q, want the default %q", laws[0].Matcher.Counts, "workspace")
	}
	if laws[0].Matcher.Key != KeyFile {
		t.Errorf("Key = %q, want %q — a ceiling is counted per root, not a multiset of text", laws[0].Matcher.Key, KeyFile)
	}
}

// TestLoadLaws_DepGraphCeilingRejectsAnUnknownCounts names the field a
// typo'd matcher.counts value is rejected under.
func TestLoadLaws_DepGraphCeilingRejectsAnUnknownCounts(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "c", `
name = "c"
description = "d"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
roots = ["account"]
counts = "everything"
`)
	if _, err := LoadLaws(dir); err == nil || !strings.Contains(err.Error(), "counts") {
		t.Fatalf("err = %v, want one naming matcher.counts", err)
	}
}

// TestLoadLaws_DepGraphCeilingRequiresRoots mirrors dep-graph-forbids'
// missing-roots rejection: a ceiling with nothing to ceiling is malformed,
// not a law that judges the whole workspace by accident.
func TestLoadLaws_DepGraphCeilingRequiresRoots(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "c", `
name = "c"
description = "d"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
`)
	if _, err := LoadLaws(dir); err == nil || !strings.Contains(err.Error(), "roots") {
		t.Fatalf("err = %v, want one naming matcher.roots", err)
	}
}
