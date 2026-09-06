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
