package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestLintWaitDeadline_HonorsItsKnob pins the exact value lintWaitDeadline
// resolves to, the same way TestPrecommitLockWait_HonorsItsKnob
// (budget_env_test.go) pins its own knob: an unset APHROLLO_LINT_WAIT_SECS
// keeps the shipped five-minute default, a set one overrides to the exact
// number of seconds named.
func TestLintWaitDeadline_HonorsItsKnob(t *testing.T) {
	t.Setenv("APHROLLO_LINT_WAIT_SECS", "")
	if got := lintWaitDeadline(); got != 300*time.Second {
		t.Fatalf("default lint wait = %s, want 5m0s", got)
	}
	t.Setenv("APHROLLO_LINT_WAIT_SECS", "45")
	if got := lintWaitDeadline(); got != 45*time.Second {
		t.Fatalf("APHROLLO_LINT_WAIT_SECS=45 → %s, want 45s", got)
	}
}

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
// exit code passes straight through, uncontended, and that an uncontended
// run — waited == 0 — prints no "lock acquired after" line: that line means
// something waited, and a run that never had to wait must not claim it did.
func TestRunGateLint_ExitCodePropagation(t *testing.T) {
	withIsolatedLintLock(t)
	fakeGolangciLint(t)

	var stdout, stderr bytes.Buffer
	code := runGateLint(fakeLintArgsExit(7), strings.NewReader(""), &stdout, &stderr)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (propagated from the stub)", code)
	}
	if strings.Contains(stderr.String(), "lock acquired after") {
		t.Fatalf("stderr = %q, an uncontended run must not claim it waited", stderr.String())
	}
}

// TestLintAcquiredLine_OnlyContendedGetsALine pins the print decision as a
// pure function of (waited, contended), deterministically — racing a real
// goroutine against AcquireLintLock's own internal timing to PROVE
// contention actually happened is exactly the kind of test that looks solid
// and is flaky in CI: whether the release() goroutine runs before or after
// AcquireLintLock's very first attempt is a scheduler decision, not
// something this test controls.
func TestLintAcquiredLine_OnlyContendedGetsALine(t *testing.T) {
	if got := lintAcquiredLine(3*time.Second, true); !strings.Contains(got, "lock acquired after 3s") {
		t.Fatalf("contended line = %q, want it to name the wait", got)
	}
	if got := lintAcquiredLine(3*time.Second, false); got != "" {
		t.Fatalf("uncontended line = %q, want \"\" (an uncontended run must not claim it waited)", got)
	}
}

// TestRunGateLint_PrintsLockAcquiredLineWhenTheAcquireReportsContended pins
// the actual wiring runGateLint does with a contended acquire, end to end:
// stub acquireLintLock's OUTCOME directly (contended=true, no real lock or
// wait involved) rather than racing a real goroutine's release() against
// AcquireLintLock's own internal timing to make this branch execute at all.
func TestRunGateLint_PrintsLockAcquiredLineWhenTheAcquireReportsContended(t *testing.T) {
	fakeGolangciLint(t)
	real := acquireLintLock
	t.Cleanup(func() { acquireLintLock = real })
	acquireLintLock = func(cmd, cwd string, deadline time.Duration) (func(), time.Duration, bool, bool) {
		return func() {}, 3 * time.Second, true, true
	}

	var stdout, stderr bytes.Buffer
	code := runGateLint(fakeLintArgsExit(0), strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "lock acquired after 3s") {
		t.Fatalf("stderr = %q, want a \"lock acquired after 3s\" line when the acquire reports contended", stderr.String())
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
