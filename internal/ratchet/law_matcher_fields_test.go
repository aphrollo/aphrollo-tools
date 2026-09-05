package ratchet

import (
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
