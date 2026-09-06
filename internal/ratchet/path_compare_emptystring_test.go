package ratchet

import (
	"path/filepath"
	"testing"
)

// pathCompareLaw is a standalone copy of the shipped path_compare law (see
// .ratchet/laws/path_compare.toml): a bare == or strings.HasPrefix against a
// path-named variable is the offence samePath/insideDir exist to answer, and
// a comparison against the empty string literal — which has no second path
// and no representation question — is excluded (#483).
const pathCompareLaw = `
name = "path-compare"
description = "a raw == or HasPrefix against a path is a Windows/unix divergence"
severity = "warn"
baseline = ".ratchet/baselines/path-compare.txt"
trigger_exclude = "==\\s*\"\"|\"\"\\s*=="

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "==\\s*\\w*[Pp]ath\\b|\\w*[Pp]ath\\s*==|strings\\.HasPrefix\\(\\w*[Pp]ath"
key = "file:line-content-hash"
`

// TestPathCompare_IgnoresComparisonAgainstEmptyStringLiteral proves #483's
// narrowing: `path == ""` has no samePath remedy (there is no second path to
// normalize) and must not be a hit, while a comparison against another path
// value stays one.
func TestPathCompare_IgnoresComparisonAgainstEmptyStringLiteral(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "path-compare", pathCompareLaw)
	write(t, filepath.Join(root, "pkg", "empty.go"), "package pkg\n\nfunc isUnset(path string) bool {\n\treturn path == \"\"\n}\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("path == \"\" has no samePath remedy and must not be a hit: %+v", res.Findings)
	}
}

// TestPathCompare_StillCatchesComparisonAgainstAnotherPath is the other
// direction the same fixture must prove: narrowing the empty-string case
// must not widen into ignoring a real path-to-path comparison.
func TestPathCompare_StillCatchesComparisonAgainstAnotherPath(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "path-compare", pathCompareLaw)
	write(t, filepath.Join(root, "pkg", "raw.go"),
		"package pkg\n\nfunc same(path, other string) bool {\n\treturn path == other\n}\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("path == otherPath is a real path comparison and must stay a hit: %+v", res.Findings)
	}
}
