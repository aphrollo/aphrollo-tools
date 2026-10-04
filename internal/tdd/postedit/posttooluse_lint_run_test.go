package postedit

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// fakeLinter puts a golangci-lint shell script on PATH alone, returning the
// directory it is in.
func fakeLinter(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake linter is a sh script") // skip-ok: the fake golangci-lint is a POSIX shell script
	}
	dir := t.TempDir()
	if err := proc.WriteExecutable(filepath.Join(dir, "golangci-lint"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

// TestGolangciLintOnPath_ReadsPATH: the linter is present exactly when a
// golangci-lint is on PATH.
func TestGolangciLintOnPath_ReadsPATH(t *testing.T) {
	fakeLinter(t, "#!/bin/sh\nexit 0\n")
	if !golangciLintOnPath() {
		t.Error("a golangci-lint on PATH was read as absent")
	}
	t.Setenv("PATH", t.TempDir())
	if golangciLintOnPath() {
		t.Error("an empty PATH was read as having golangci-lint")
	}
}

// TestRunLintWithin_ReturnsWhatTheLinterPrintedAndNoTimeout: a finding exits
// non-zero, and its text is what comes back, with no timeout.
// ratchet: test_removed TestRunLintWithin_ReturnsWhatTheLinterPrintedAndNoTimeout: the in-hook lint runner is gone with the edit-time lint
// ratchet: test_removed TestRunLintWithin_CutsOffARunPastItsBudget: the in-hook lint runner is gone with the edit-time lint
// ratchet: test_removed internal/tdd/postedit/lintdeferred_test.go: the detached edit-time lint and its harvester are gone; the run carries the lint (lintrun.go)
// ratchet: test_removed internal/tdd/postedit/posttooluse_lint_test.go: the in-hook edit-time lint is gone; the run carries the lint (lintrun.go)
// ratchet: test_removed internal/tdd/postedit/posttooluse_lint_tree_test.go: the in-hook lint runner is gone with the edit-time lint

// TestLoadFromProc_ReadsTheFirstFieldAndStandsDownOnAnythingElse pins what
// counts as a readable load: the first field of the file, as a number.
func TestLoadFromProc_ReadsTheFirstFieldAndStandsDownOnAnythingElse(t *testing.T) {
	read := func(s string, err error) func() ([]byte, error) {
		return func() ([]byte, error) { return []byte(s), err }
	}
	load, cores, ok := loadFromProc(read("3.50 2.10 1.00 1/200 999\n", nil))
	if !ok || load != 3.5 || cores != runtime.NumCPU() {
		t.Errorf("readable: load %v cores %d ok %v; want 3.5, %d, true", load, cores, ok, runtime.NumCPU())
	}
	for name, r := range map[string]func() ([]byte, error){
		"a read error": read("3.50 2.10", errors.New("no such file")),
		"an empty":     read("  \n", nil),
		"a non-number": read("busy 2.10 1.00", nil),
	} {
		if _, _, ok := loadFromProc(r); ok {
			t.Errorf("%s file was read as a load", name)
		}
	}
}

// TestWithinBackoff_HoldsUntilExactlyTheBackoffHasPassed pins the edge of
// the window.
func TestWithinBackoff_HoldsUntilExactlyTheBackoffHasPassed(t *testing.T) {
	if !withinBackoff(lintEditBackoff - time.Nanosecond) {
		t.Error("one nanosecond short of the backoff must still hold")
	}
	if withinBackoff(lintEditBackoff) {
		t.Error("a full backoff old must no longer hold")
	}
}

// TestLintBackedOff_ReadsTheMarkerOfItsOwnRoot: a marker touched now backs
// off its own root and no other; an old marker no longer does.
func TestLintBackedOff_ReadsTheMarkerOfItsOwnRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, other := t.TempDir(), t.TempDir()
	if lintBackedOff(root) {
		t.Fatal("a root with no marker was backed off")
	}
	markLintBackoff(root)
	if !lintBackedOff(root) || lintBackedOff(other) {
		t.Fatalf("after the mark: root backed off %v, other root %v; want true, false", lintBackedOff(root), lintBackedOff(other))
	}
	old := time.Now().Add(-lintEditBackoff)
	if err := os.Chtimes(lintBackoffMarker(root), old, old); err != nil {
		t.Fatal(err)
	}
	if lintBackedOff(root) {
		t.Fatal("a marker a full backoff old still backed the root off")
	}
}
