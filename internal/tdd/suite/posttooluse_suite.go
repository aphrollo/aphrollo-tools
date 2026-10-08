package suite

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
	"github.com/aphrollo/aphrollo-tools/internal/run"
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
	// Inconclusive is why a run that never reached a verdict was not judged
	// on the code: the memory cap ended it (`OOM-KILLED at 11.6 GB`) or the box
	// had no memory to start it (`SKIPPED — memory headroom: …`). It is set
	// together with TimedOut, so every consumer that already refuses to read a
	// timeout as red reads this the same way, and the reporters print this
	// text instead of the word TIMEOUT. "" for every run that reached a verdict.
	Inconclusive string
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
	// SplitNote is what a list cut into several runs (splitrun.go) left
	// undone when it did not finish: the runs that timed out or were ended, by
	// number and packages, and the runs that never started. "" for a run that
	// was one command, and for a split list that finished.
	SplitNote string
}

// SuiteRunner executes a runner in a project root. It is injected so the
// orchestration can be tested without spawning real test suites.
type SuiteRunner func(r Runner, root string) SuiteResult

// cmdString renders a Runner as the "<cmd> <args>" text every advisory line
// uses, trimmed so a runner with no args never leaves a trailing space.
func cmdString(r Runner) string {
	args := r.Args
	if r.Select != nil && r.Select.Run > 0 {
		// A selection's pattern names every test it runs: logs and lines say
		// how many instead, and the argv the run executes stays complete.
		args = slices.Clone(args)
		for i, a := range args {
			if strings.HasPrefix(a, "-run=^(") {
				args[i] = fmt.Sprintf("-run=<%d tests>", r.Select.Run)
			}
		}
	}
	return strings.TrimSpace(r.Cmd + " " + strings.Join(args, " "))
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
//
// vitest and jest have the same shape (#1245): handed a file their own include
// pattern does not match, such as a Playwright spec under e2e/, they exit 1
// with "No test files found" / "No tests found, exiting with code 1" having
// run nothing. That is an empty run, never a red.
var noTestsToRunRe = regexp.MustCompile(`(?i)no tests to run|no test files found|no tests found, exiting with code 1`)

// RunSuite is the production SuiteRunner: it executes the test command with a
// bounded timeout and a quiet, deterministic environment (CI=1, NO_COLOR=1),
// combining stdout and stderr. A timeout or signal is reported as NOT passed.
// A command line past what the platform can start runs as several, each
// within budget (runsuite_batched.go).
func RunSuite(timeout time.Duration) SuiteRunner {
	one := runSuiteOnce(timeout)
	return func(r Runner, root string) SuiteResult {
		return runRapidSplit(r, root, timeout, func(sub Runner, dir string) SuiteResult {
			return runImpureSplit(sub, dir, timeout, argvBudgetFn(sub.Cmd), one)
		})
	}
}

// runSuiteOnce is RunSuite's one spawn: the runner's command line exactly as
// it stands.
func runSuiteOnce(timeout time.Duration) SuiteRunner {
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
		// Runner.Dir overrides the execution directory when set (a resolved
		// cargo workspace runner: a checked-in .config/nextest.toml and the
		// workspace's Cargo.lock live at the workspace root, not a member
		// crate's own directory) — every other runner leaves it "" and falls
		// back to root, exactly as before Runner.Dir existed.
		dir := root
		if r.Dir != "" {
			dir = r.Dir
		}
		// The budget starts here, before that wait, so what the wait spends is
		// spent out of the run's own time and not on top of it.
		budgetStart := suiteNowFn()
		// A start waits for the box to have memory to give it, inside its own
		// budget, and is refused with the reason when it never does: a suite
		// started into an exhausted box is how a runaway takes sessions with it.
		if why := waitForHeadroomFn(dir, headroomWait(effectiveTimeout)); why != "" {
			why = "SKIPPED — " + why
			return SuiteResult{TimedOut: true, Inconclusive: why, Err: why, Dir: dir}
		}
		// Every runner here is a LAUNCHER: `go test` compiles a test binary and
		// runs it as a grandchild, cargo spawns rustc. run ends the whole tree
		// at the deadline, so none of them is left alive holding build outputs
		// for the rest of the session (stranded `<pkg>.test.exe` processes
		// from earlier timed-out runs, and MSYS grandchildren `taskkill /T`
		// cannot reach). run also gives the output pipes a grace after the exit,
		// so a killed runner's surviving children cannot hold the read past the
		// deadline.
		var buf bytes.Buffer
		start := time.Now()
		end := suiteChildFn(run.Spec{
			Name: r.Cmd, Args: goExecArgsFor(r), Dir: dir, Env: suiteEnv(r, dir),
			Stdout: &buf, Stderr: &buf, Timeout: effectiveTimeout - suiteNowFn().Sub(budgetStart),
		}, MemCapFor(dir, CapSlot))
		err, capped, timedOut := end.err, end.capped, end.timedOut
		out := buf.Bytes()
		dur := time.Since(start)
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
		res := SuiteResult{Passed: passed, Output: outputText, TimedOut: timedOut, Err: errText, Duration: dur, GoTestJSON: testJSON, Dir: dir}
		if capped.Killed {
			// The cap ended it: what the run printed and exited with is the
			// kill's doing, so it is inconclusive, never red and never a
			// timeout.
			res.Passed, res.TimedOut, res.Inconclusive = false, true, capped.Line()
		}
		if why := StartFailure(err, r.Cmd, func() string { return outputText }, os.Getenv("PATH")); why != "" {
			// The command never started, or a shell around it could not find
			// one: nothing ran that could have failed, so this is no red.
			res.Passed, res.TimedOut, res.Inconclusive = false, true, why
		}
		return res
	}
}

// headroomWaitCeiling bounds how long a start waits for memory: a fraction of
// the run's own budget, never more than this.
const headroomWaitCeiling = 2 * time.Minute

// headroomWait is how long a run with this budget may wait for memory before
// it is refused: a quarter of the budget, so most of it is left to run in.
func headroomWait(budget time.Duration) time.Duration {
	return min(budget/4, headroomWaitCeiling)
}

// suiteEnv is the environment for the suite subprocess: a quiet, deterministic
// shell (CI=1, NO_COLOR=1) with every GIT_* variable scrubbed. The gate runs as
// a git pre-commit hook, so os.Environ() carries GIT_DIR / GIT_INDEX_FILE /
// GIT_WORK_TREE pointing at the repo being committed; leaking them into the
// suite's `go test` makes its git-e2e fixtures commit against the WRONG repo and
// clobber its HEAD. cleanGitEnv (in precommit.go, same package) drops them so the
// suite runs as if invoked from a plain shell.
//
// A `go` runner (and golangci-lint, which runs the go tool) also gets
// goTmpEnv(dir), and a cargo runner cargoTmpEnv(dir): otherwise `go test` stages its
// compiled test binary under the OS temp dir, which is how tdd.test.exe ended
// up Defender-quarantined and 133 go-build* dirs survived killed runs (issue
// #520) and a workspace build's rustc scratch filled a RAM-backed /tmp (issue
// #1005). Every other runner (pytest, vitest, ...) is left exactly as it was.
//
// The runner's own Env comes last, so a binding there wins over an inherited
// one.
func suiteEnv(r Runner, dir string) []string {
	env := append(cleanGitEnv(), "CI=1", "NO_COLOR=1")
	var scratch []string
	switch r.Cmd {
	case "go", "golangci-lint":
		// golangci-lint drives the go tool for its package loading, so its
		// go-build scratch follows the same variable.
		scratch = goTmpEnv(dir)
		scratch = append(scratch, goTrimpathEnv(os.Environ(), dir)...)
	case "cargo":
		scratch = cargoTmpEnv(dir)
	}
	env = append(env, scratch...)
	// A run with a scratch dir of its own has its git sealed to it: no test
	// there can find a repository by walking up from its temp dirs, and the
	// global git config it writes is an empty file, never the operator's.
	for _, kv := range scratch {
		if area, ok := strings.CutPrefix(kv, "TMPDIR="); ok {
			env = gitenv.Sealed(env, area)
		}
	}
	return append(env, r.Env...)
}
