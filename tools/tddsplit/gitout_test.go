package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGitOnPath builds a `git` executable in a fresh t.TempDir() that prints
// stdout on the first line and a stderr warning on the second, then prepends
// that dir to PATH for the duration of the test. Cleaned up automatically:
// t.TempDir() and t.Setenv() both unwind at test end.
func fakeGitOnPath(t *testing.T, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	name := "git"
	script := "#!/bin/sh\nprintf '%s' \"$STDOUT_LINE\"\nprintf '%s' \"$STDERR_LINE\" 1>&2\n"
	if runtime.GOOS == "windows" {
		name = "git.bat"
		script = "@echo off\r\n<nul set /p=%STDOUT_LINE%\r\n<nul set /p=%STDERR_LINE% 1>&2\r\n"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STDOUT_LINE", stdout)
	t.Setenv("STDERR_LINE", stderr)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGitOut_ReturnsStdoutOnlyEvenWithAStderrWarning is the regression for
// #869: a git-queue shim printing "gate: <bin> is missing — running git
// UNGATED" on stderr must never land inside data gitOut's caller compares
// byte for byte (e.g. `status --porcelain`'s dirty-tree check).
func TestGitOut_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	fakeGitOnPath(t, "clean-stdout\n", "gate: aphrollo is missing — running git UNGATED\n")
	got, err := gitOut(t.TempDir(), "status", "--porcelain")
	if err != nil {
		t.Fatalf("gitOut: %v", err)
	}
	if got != "clean-stdout\n" {
		t.Fatalf("gitOut = %q, want stdout only (no stderr warning mixed in)", got)
	}
	if strings.Contains(got, "UNGATED") {
		t.Fatalf("gitOut leaked stderr into its data return: %q", got)
	}
}
