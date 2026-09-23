package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// exLintTempFail is sysexits.h's EX_TEMPFAIL, the same code the cargo shim
// uses for its own give-up-waiting outcome: the lint lock is fine, this
// invocation just could not get it in time, which is not the same failure
// as golangci-lint itself finding something.
const exLintTempFail = 75

// defaultLintWait bounds how long `gate lint` waits for the box-wide
// lint lock before giving up. Five minutes, the same default the local
// commit gate's own wait uses — the two entry points wait on the identical
// lock for the identical reason, so they share the default rather than
// inventing a second number.
const defaultLintWait = 300 * time.Second

// lintWaitDeadline resolves how long `gate lint` waits for the box-wide
// lint lock, through the SAME parser every other operator budget knob in
// this package uses (envDurationSecs, cli_gate.go) rather than a second
// hand-rolled one: APHROLLO_LINT_WAIT_SECS when it names a whole, non-negative
// number of seconds, else defaultLintWait.
func lintWaitDeadline() time.Duration {
	return envDurationSecs("APHROLLO_LINT_WAIT_SECS", defaultLintWait)
}

// runGateLint is `aphrollo gate lint <golangci-lint args...>`: the ONE entry
// point both this box's local commit gate and CI's self-hosted `lint` job
// (pipeline.yml) use to invoke golangci-lint, so a runner-user lint and a
// debian-user lint reach the SAME cross-account lock (internal/tdd's
// lockDir(), /var/tmp/aphrollo-locks in production) instead of colliding on
// golangci-lint's OWN $TMPDIR lock — which is opened 0600 by whichever
// account gets there first and denies every OTHER account a hard permission
// error, reported identically to ordinary contention ("parallel
// golangci-lint is running") whether --allow-serial-runners was passed or
// not.
//
// An uncontended lock is silent (acquire, run, done); a contended one
// announces once it finally gets the lock. AcquireLintLock's own contended
// return value is the fact this decision rests on, never a duration compared
// against a guessed threshold: real wall-clock time always advances some
// nonzero amount even on an uncontended first try, so `waited > 0` looked
// right but was true for every run, contended or not.
func runGateLint(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aphrollo gate lint <golangci-lint args...>")
		return 64
	}
	binPath, err := exec.LookPath("golangci-lint")
	if err != nil {
		fmt.Fprintf(stderr, "gate lint: golangci-lint not found on PATH: %v\n", err)
		return 1
	}
	cwd, _ := os.Getwd()
	cmdLine := binPath + " " + strings.Join(args, " ")
	release, waited, contended, ok := tdd.AcquireLintLock(cmdLine, cwd, lintWaitDeadline())
	if !ok {
		fmt.Fprintf(stderr, "gate lint: gave up waiting %.0fs for the box-wide lint lock (held by %s) — nothing was linted\n",
			waited.Seconds(), tdd.LintLockHolderDescription())
		return exLintTempFail
	}
	defer release()
	if line := lintAcquiredLine(waited, contended); line != "" {
		fmt.Fprint(stderr, line)
	}
	return execGolangciLint(binPath, args, stdin, stdout, stderr)
}

// lintAcquiredLine composes the ONE-SHOT "lock acquired after" line for a
// run that had to wait, "" for one that did not — pulled out of runGateLint
// so the print decision is a pure function of (waited, contended) a test can
// call directly, rather than something provable only by racing a real
// goroutine against AcquireLintLock's own internal timing.
func lintAcquiredLine(waited time.Duration, contended bool) string {
	if !contended {
		return ""
	}
	return fmt.Sprintf("gate lint: lock acquired after %.0fs\n", waited.Seconds())
}

// execGolangciLint runs the wrapped golangci-lint with real stdio, once this
// process holds the box-wide lint lock, and propagates its exit code.
func execGolangciLint(binPath string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "gate lint: %v\n", err)
		return 1
	}
	return 0
}
