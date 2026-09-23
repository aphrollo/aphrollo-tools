package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// withIsolatedLintLock points the lint lock (internal/tdd's lockDir) at a
// per-test directory, the same way cargo_shim_test.go's withIsolatedCargoLock
// isolates the build lock: without it these tests would contend with the
// box's own real lint lock, or with the aphrollo PostToolUse hook's own lint
// run exercising the SAME lock this very edit is adding.
func withIsolatedLintLock(t *testing.T) {
	t.Helper()
	restore := tdd.SetLockDirForTest(t.TempDir())
	t.Cleanup(restore)
}

// fakeGolangciLint puts an `sh`-backed stand-in named "golangci-lint" ahead
// of PATH, so runGateLint's exec.LookPath("golangci-lint") resolves to
// something these tests control the exit code of, without needing the real
// linter installed.
func fakeGolangciLint(t *testing.T) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("no sh on PATH to stand in for golangci-lint: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(sh, filepath.Join(dir, "golangci-lint")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fakeLintArgsExit(code int) []string {
	return []string{"-c", "exit " + strconv.Itoa(code)}
}

// TestRunGateLint_ExitCodePropagation pins that the wrapped golangci-lint's
// exit code passes straight through, uncontended.
func TestRunGateLint_ExitCodePropagation(t *testing.T) {
	withIsolatedLintLock(t)
	fakeGolangciLint(t)

	var stdout, stderr bytes.Buffer
	code := runGateLint(fakeLintArgsExit(7), strings.NewReader(""), &stdout, &stderr)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (propagated from the stub)", code)
	}
}

// TestRunGateLint_GivesUpWhenTheLockStaysHeld pins the reason this wrapper
// exists: a lint already running (this gate's own, or CI's) must make a
// second `gate lint` WAIT for the box-wide lint lock rather than let both
// reach golangci-lint's own internal lock at the same moment — and a wait
// that runs out its budget refuses cleanly (EX_TEMPFAIL) instead of running
// unlocked, which would be the exact collision this wrapper exists to
// remove.
func TestRunGateLint_GivesUpWhenTheLockStaysHeld(t *testing.T) {
	withIsolatedLintLock(t)
	fakeGolangciLint(t)
	t.Setenv("APHROLLO_LINT_WAIT_SECS", "0")

	release, ok := tdd.TryAcquireLintLock("holder", "/repo/holder")
	if !ok {
		t.Fatal("setup: could not take the lint lock directly")
	}
	defer release()

	var stdout, stderr bytes.Buffer
	code := runGateLint(fakeLintArgsExit(0), strings.NewReader(""), &stdout, &stderr)
	if code != exLintTempFail {
		t.Fatalf("exit = %d, want %d (EX_TEMPFAIL: gave up waiting for the lock)", code, exLintTempFail)
	}
}
