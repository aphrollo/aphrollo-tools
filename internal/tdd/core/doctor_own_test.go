package core

import (
	"path/filepath"
	"testing"
)

func TestSamePath_NormalizesSeparatorsAndQuoting(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "repo")
	if !samePath(a, a+"/") {
		t.Fatal("a trailing slash must not change identity")
	}
	if !samePath(`"`+a+`"`, a) {
		t.Fatal("surrounding quotes must be trimmed before comparing")
	}
	if !samePath(a, filepath.Join(dir, "x", "..", "repo")) {
		t.Fatal("a non-cleaned but equivalent path must still match")
	}
	if samePath(a, filepath.Join(dir, "other")) {
		t.Fatal("genuinely different paths must not match")
	}
}

func TestSamePath_CaseSensitiveOnNonWindows(t *testing.T) {
	// This suite runs on Linux; case must matter there (only Windows folds
	// case), so two differently-cased spellings of the same directory name
	// must NOT be treated as the same path.
	if samePath("/tmp/Repo", "/tmp/repo") {
		t.Fatal("case must matter on a non-Windows OS")
	}
}

func TestSortStrings_SortsInPlace(t *testing.T) {
	cases := [][]string{
		{"banana", "apple", "cherry"},
		{"only"},
		{},
		{"b", "a", "a", "c"},
	}
	for _, in := range cases {
		got := append([]string(nil), in...)
		sortStrings(got)
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Fatalf("sortStrings left %v unsorted at index %d", got, i)
			}
		}
		if len(got) != len(in) {
			t.Fatalf("sortStrings changed the length: got %v from %v", got, in)
		}
	}
}
