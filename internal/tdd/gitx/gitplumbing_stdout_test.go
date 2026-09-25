package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeGitPrinting builds a `git` script in t.TempDir() that prints stdout on
// its own line and a stderr warning on another, exits 0, and points
// APHROLLO_REAL_GIT at it for the duration of the test. Cleaned up
// automatically: t.TempDir()/t.Setenv() both unwind at test end, no leftover
// process or file.
func fakeGitPrinting(t *testing.T, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-git.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"" + stdout + "\"\nprintf '%s\\n' \"" + stderr + "\" 1>&2\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(realGitEnv, path)
}

// TestGit_ReturnsStdoutOnlyEvenWithAStderrWarning is the gitx half of #869: a
// stderr warning from the child git (e.g. a queue shim's "gate: <bin> is
// missing — running git UNGATED") must never land inside the value a DATA
// caller (rev-parse, diff --cached --name-status, hash-object) reads byte
// for byte.
func TestGit_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	fakeGitPrinting(t, "deadbeef", "gate: aphrollo is missing — running git UNGATED")
	got, err := git(t.TempDir(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	if got != "deadbeef\n" {
		t.Fatalf("git() = %q, want stdout only", got)
	}
}

// TestGitStdin_ReturnsStdoutOnlyEvenWithAStderrWarning covers the stdin-fed
// path (hash-object --stdin), the exact call mutationLandedEvidence makes to
// compare two content hashes — a stderr line mixed into either hash would
// make an unmutated file read as changed.
func TestGitStdin_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	fakeGitPrinting(t, "cafef00d", "warning: CRLF will be replaced by LF")
	got, err := gitStdin(t.TempDir(), nil, "hash-object", "--stdin")
	if err != nil {
		t.Fatalf("gitStdin: %v", err)
	}
	if got != "cafef00d\n" {
		t.Fatalf("gitStdin() = %q, want stdout only", got)
	}
}
