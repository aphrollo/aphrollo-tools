package core

import (
	"path/filepath"
	"testing"
)

func TestNormalizeProjectPath_AbsoluteAndCleaned(t *testing.T) {
	dir := t.TempDir()
	messy := filepath.Join(dir, "a", "..", "b")
	got := normalizeProjectPath(messy)
	want := filepath.Join(dir, "b")
	if got != want {
		t.Fatalf("normalizeProjectPath(%q) = %q, want %q", messy, got, want)
	}

	// Two spellings of the same directory normalize identically.
	if normalizeProjectPath(filepath.Join(dir, "b")+"/") != want {
		t.Fatal("a trailing slash must normalize to the same path")
	}
}

func TestProjectKey_StableUniqueHash(t *testing.T) {
	a1 := projectKey("/proj/a")
	a2 := projectKey("/proj/a")
	b := projectKey("/proj/b")

	if a1 != a2 {
		t.Fatalf("projectKey must be stable for the same root: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Fatalf("two different projects must not share a key: %q", a1)
	}
	if len(a1) != 16 {
		t.Fatalf("projectKey length = %d, want 16 (8 bytes, hex)", len(a1))
	}
}
