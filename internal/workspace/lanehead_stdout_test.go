package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeGitPrinting builds a `git` script in t.TempDir() that prints stdout on
// its own line and a stderr warning on another, exits 0, and prepends that
// dir to PATH for the test's duration. Cleaned up automatically:
// t.TempDir()/t.Setenv() both unwind at test end, no leftover process or file.
func fakeGitPrinting(t *testing.T, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"" + stdout + "\"\nprintf '%s\\n' \"" + stderr + "\" 1>&2\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestLaneHeadSHA_ReturnsStdoutOnlyEvenWithAStderrWarning: laneHeadSHA's
// result is compared byte for byte against a PR's HeadSHA. A stderr warning
// from a git-queue shim (e.g. "gate: <bin> is missing — running git
// UNGATED") folded into that value by CombinedOutput would make a lane whose
// head genuinely matches the PR read as never caught up.
func TestLaneHeadSHA_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	fakeGitPrinting(t, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "gate: aphrollo is missing — running git UNGATED")
	got, err := laneHeadSHA(t.TempDir())
	if err != nil {
		t.Fatalf("laneHeadSHA: %v", err)
	}
	if got != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("laneHeadSHA = %q, want stdout only", got)
	}
}
