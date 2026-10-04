package git

import (
	"os"
	"path/filepath"
	"testing"
)

// looksBare is git's own test for a git directory: a HEAD file, an objects
// directory and a refs directory, all three.
func TestLooksBare_NeedsAllThreeParts(t *testing.T) {
	full := t.TempDir()
	write(t, full, "HEAD", "ref: refs/heads/main\n")
	for _, d := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(full, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if !looksBare(full) {
		t.Fatal("a directory with HEAD, objects and refs is a git directory")
	}
	for _, missing := range []string{"HEAD", "objects", "refs"} {
		dir := t.TempDir()
		parts := map[string]bool{"HEAD": true, "objects": true, "refs": true}
		delete(parts, missing)
		for part := range parts {
			if part == "HEAD" {
				write(t, dir, "HEAD", "ref: refs/heads/main\n")
			} else if err := os.MkdirAll(filepath.Join(dir, part), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if looksBare(dir) {
			t.Errorf("a directory with no %s was taken for a git directory", missing)
		}
	}
	// a file where a directory belongs is not one either
	odd := t.TempDir()
	write(t, odd, "HEAD", "x")
	write(t, odd, "objects", "x")
	write(t, odd, "refs", "x")
	if looksBare(odd) {
		t.Error("files named objects and refs were taken for directories")
	}
}

func TestFirstToken_OfNothingIsNothing(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"   \n":                   "",
		"abc123":                  "abc123",
		"abc123\ndef456\n":        "abc123",
		"abc123\tnot-for-merge x": "abc123",
	} {
		if got := firstToken(in); got != want {
			t.Errorf("firstToken(%q) = %q, want %q", in, got, want)
		}
	}
}
