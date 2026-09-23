package tddtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// GitDo runs one git command in dir and fails the test when it fails.
func GitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitBin(), args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// GitAddAll stages everything under root.
func GitAddAll(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command(gitBin(), "add", "-A")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
}

// CommitAll commits what is staged under root, past every hook.
func CommitAll(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command(gitBin(), "-c", "core.hooksPath=", "commit", "-q", "-m", "fixture", "--no-verify")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

// GitNote is the note under notesRef on rev, "" when there is none.
func GitNote(t *testing.T, notesRef, dir, rev string) string {
	t.Helper()
	cmd := exec.Command(gitBin(), "notes", "--ref="+notesRef, "show", rev)
	cmd.Dir = dir
	// stderr-ok: a rev with no note exits non-zero, and that absence is the answer
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitOutT is a git value a test needs to compare against.
func GitOutT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBin(), args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v%s", args, err, exitStderr(err))
	}
	return strings.TrimSpace(string(out))
}

// exitStderr is what a failed child wrote to stderr, on a line of its own,
// "" when err carries none.
func exitStderr(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return "\n" + string(ee.Stderr)
	}
	return ""
}

// GitValue reads one git value in a test, without going through the helper
// under test.
func GitValue(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBin(), append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v%s", args, err, exitStderr(err))
	}
	return strings.TrimSpace(string(out))
}

// CurrentBranch returns repoRoot's current branch name, whatever git init
// picked as the default (varies by git version/config) -- tests that need
// to check out BACK to the starting branch read this instead of hardcoding
// "main" or "master". git is the package's own git runner.
func CurrentBranch(t *testing.T, repoRoot string, git func(dir string, args ...string) (string, error)) string {
	t.Helper()
	out, err := git(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse --abbrev-ref HEAD: %v: %s", err, out)
	}
	return strings.TrimSpace(out)
}

// FakeGitShim writes a git that is NOT git: it records that it ran and fails.
// It carries the same tell the real queue shim does — it re-enters aphrollo —
// which is what marks a PATH entry as a shim dir rather than a git install.
func FakeGitShim(t *testing.T) (dir, marker string) {
	t.Helper()
	dir = t.TempDir()
	marker = filepath.Join(dir, "shim-ran.txt")
	cmd := "@echo off\r\nrem aphrollo git queue shim\r\necho %* > \"" + marker + "\"\r\nexit /b 128\r\n"
	if err := os.WriteFile(filepath.Join(dir, "git.cmd"), []byte(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := "#!/bin/sh\n# aphrollo git queue shim\necho \"$@\" > \"" + marker + "\"\nexit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, marker
}
