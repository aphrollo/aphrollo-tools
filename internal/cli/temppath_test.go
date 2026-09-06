package cli

import (
	"path/filepath"
	"testing"
)

// resolvedTempDir is t.TempDir() with every symlink and short name expanded.
//
// GitHub's Windows runner sets TMP to the 8.3 SHORT form
// (C:\Users\RUNNER~1\...), which t.TempDir() inherits. Production code that
// canonicalises a path returns the LONG form (C:\Users\runneradmin\...), so a
// fixture root taken straight from t.TempDir() no longer matches what the code
// under test reports, even though both name the same directory. Resolve once
// here so the comparison is like-for-like rather than loosened at the
// assertion.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return dir
}
