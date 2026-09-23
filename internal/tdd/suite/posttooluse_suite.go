package suite

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// SuiteResult is the outcome of executing a Runner.
type SuiteResult struct {
	Passed bool
	Output string
	// TimedOut marks a run killed at its deadline. A timeout says nothing
	// about the code under test, so every consumer treats it as inconclusive
	// — PostEdit stays silent, the mechanical gate does not block, fail-first
	// reaches no verdict. Without the signal a slow suite reads as RED and
	// the gates nag or block over a stopwatch, not a failure.
	TimedOut bool
	// Err is the runner-level error text ("" when the run completed cleanly):
	// an exit status, a spawn failure, or Go's wait-delay note. It is what the
	// mechanical gate surfaces when the output itself names no failing test —
	// a rejection must say WHAT failed, not just "failing".
	Err string
	// Duration is how long the run took wall-clock, set by the SuiteRunner
	// (RunSuite: time.Since(start), including a killed run — a TimedOut result
	// reports roughly the configured budget). Every advisory/stage line that
	// reports "Ns" reads this field rather than re-timing itself, so a fake
	// SuiteRunner in a test can pin an exact duration deterministically.
	Duration time.Duration
	// GoTestJSON is the raw `go test -json` event stream, set by RunSuite
	// only for a go test invocation (empty otherwise). Output stays
	// reconstructed human text for existing consumers; vacuousGoPackages
	// reads this field to attribute a pass to its actual package.
	GoTestJSON string
	// Dir is the directory the run executed in, set by whatever started it
	// (RunSuite, phaseSuiteResult). It is not the root a verdict is keyed
	// on: a cargo run executes in its workspace directory, above the member
	// crate that names it, and the retained record states both so a reader
	// can tell which tree was tested (issue #769).
	Dir string
}

// SuiteRunner executes a runner in a project root. It is injected so the
// orchestration can be tested without spawning real test suites.
type SuiteRunner func(r Runner, root string) SuiteResult

// cmdString renders a Runner as the "<cmd> <args>" text every advisory line
// uses, trimmed so a runner with no args never leaves a trailing space.
func cmdString(r Runner) string {
	return strings.TrimSpace(r.Cmd + " " + strings.Join(r.Args, " "))
}

// noTestsToRunRe recognises cargo-nextest's hard failure (exit code 4) when a
// scope selects ZERO tests — a dependency-only crate (e.g. a cargo-hakari
// workspace-hack crate, which is deliberately never given a test target) hits
// this on every commit that touches it. Semantically that is an EMPTY PASS
// ("this crate has no tests"), not a failure: plain `cargo test` already
// exits 0 for the identical situation (ClassifyOutcome's zeroTestsRe already
// covers that case via "no tests? (?:found|to run|...)"). nextest is the one
// runner that turns "nothing to run" into a nonzero exit and a Passed=false
// SuiteResult, which without this check hard-blocked the commit as "tests
// failing" over a crate that was never supposed to have any.
var noTestsToRunRe = regexp.MustCompile(`(?i)no tests to run`)

// RunSuite is the production SuiteRunner: it executes the test command with a
// bounded timeout and a quiet, deterministic environment (CI=1, NO_COLOR=1),
// combining stdout and stderr. A timeout or signal is reported as NOT passed.
func RunSuite(timeout time.Duration) SuiteRunner {
	return func(r Runner, root string) SuiteResult {
		// Runner.Deadline (set by runCargoLocked BEFORE it waits for the
		// machine-wide build lock) carves that wait OUT of this run's own
		// budget instead of it stacking on top — use whichever bound is
		// EARLIER: the configured timeout, or time-until-Deadline. A zero
		// Deadline (every runner except a locked cargo one) leaves timeout
		// unchanged.
		effectiveTimeout := timeout
		if !r.Deadline.IsZero() {
			if remaining := time.Until(r.Deadline); remaining < effectiveTimeout {
				effectiveTimeout = remaining
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), effectiveTimeout)
		defer cancel()
		// Runner.Dir overrides the execution directory when set (a resolved
		// cargo workspace runner: a checked-in .config/nextest.toml and the
		// workspace's Cargo.lock live at the workspace root, not a member
		// crate's own directory) — every other runner leaves it "" and falls
		// back to root, exactly as before Runner.Dir existed.
		dir := root
		if r.Dir != "" {
			dir = r.Dir
		}
		cmd := exec.CommandContext(ctx, r.Cmd, goExecArgs(r.Cmd, r.Args)...)
		cmd.Dir = dir
		cmd.Env = suiteEnv(r, dir)
		// The default cancel kills the direct child and nothing else, and
		// every runner here is a LAUNCHER: `go test` compiles a test binary
		// and runs it as a grandchild, cargo spawns rustc. Killing the
		// launcher left those alive for the rest of the session, holding
		// build outputs and polling — measured as stranded `<pkg>.test.exe`
		// processes from earlier timed-out runs. proc.KillTree is the same
		// reach a deferred phase already uses.
		cmd.SysProcAttr = suiteAttrs()
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return proc.KillTree(cmd.Process.Pid)
		}
		// Without WaitDelay a killed test runner's surviving children hold the
		// output pipes open and CombinedOutput blocks long past the deadline
		// (cmd.exe's children on Windows, orphaned workers elsewhere).
		cmd.WaitDelay = 2 * time.Second
		start := time.Now()
		out, err := cmd.CombinedOutput()
		dur := time.Since(start)
		timedOut := ctx.Err() == context.DeadlineExceeded
		// ErrWaitDelay means the process EXITED SUCCESSFULLY but an orphaned
		// child held the I/O pipes past WaitDelay — Go returns it INSTEAD of
		// nil in that case. The suite's own verdict is green; treating the
		// pipe-holder as RED manufactured phantom "tests failing" blocks.
		passed := (err == nil || errors.Is(err, exec.ErrWaitDelay)) && !timedOut
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		outputText, testJSON := goRenderedOutput(r.Cmd, r.Args, string(out))
		return SuiteResult{Passed: passed, Output: outputText, TimedOut: timedOut, Err: errText, Duration: dur, GoTestJSON: testJSON, Dir: dir}
	}
}

// suiteEnv is the environment for the suite subprocess: a quiet, deterministic
// shell (CI=1, NO_COLOR=1) with every GIT_* variable scrubbed. The gate runs as
// a git pre-commit hook, so os.Environ() carries GIT_DIR / GIT_INDEX_FILE /
// GIT_WORK_TREE pointing at the repo being committed; leaking them into the
// suite's `go test` makes its git-e2e fixtures commit against the WRONG repo and
// clobber its HEAD. cleanGitEnv (in precommit.go, same package) drops them so the
// suite runs as if invoked from a plain shell.
//
// A `go` runner also gets goTmpEnv(dir): otherwise `go test` stages its
// compiled test binary under the OS temp dir, which is how tdd.test.exe ended
// up Defender-quarantined and 133 go-build* dirs survived killed runs (issue
// #520). Every other runner (cargo, pytest, vitest, ...) is left exactly as
// it was — cargo's own target dir already lands inside the project, so it has
// no analogous problem to fix.
func suiteEnv(r Runner, dir string) []string {
	env := append(cleanGitEnv(), "CI=1", "NO_COLOR=1")
	if r.Cmd == "go" {
		env = append(env, goTmpEnv(dir)...)
	}
	return env
}
