package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// exLintTempFail is sysexits.h's EX_TEMPFAIL, the same code the cargo shim
// uses for its own give-up-waiting outcome: the lint lock is fine, this
// invocation just could not get it in time, which is not the same failure
// as golangci-lint itself finding something.
const exLintTempFail = 75

// defaultLintWaitSecs bounds how long `gate lint` waits for the box-wide
// lint lock before giving up. Five minutes, the same default the local
// commit gate's own wait uses — the two entry points wait on the identical
// lock for the identical reason, so they share the default rather than
// inventing a second number.
const defaultLintWaitSecs = 300

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
	deadline := defaultLintWaitSecs * time.Second
	if raw := strings.TrimSpace(os.Getenv("APHROLLO_LINT_WAIT_SECS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			deadline = time.Duration(n) * time.Second
		}
	}
	cwd, _ := os.Getwd()
	cmdLine := binPath + " " + strings.Join(args, " ")
	release, waited, ok := tdd.AcquireLintLock(cmdLine, cwd, deadline)
	if !ok {
		fmt.Fprintf(stderr, "gate lint: gave up waiting %.0fs for the box-wide lint lock (held by %s) — nothing was linted\n",
			waited.Seconds(), tdd.LintLockHolderDescription())
		return exLintTempFail
	}
	defer release()
	if waited > 0 {
		fmt.Fprintf(stderr, "gate lint: lock acquired after %.0fs\n", waited.Seconds())
	}
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
