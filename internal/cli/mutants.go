package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runPostCommit is the `gate postcommit` git hook: it starts the lane's
// mutation run and returns. It NEVER blocks and never reports failure — the
// commit has already happened by the time this runs, so a non-zero exit would
// only print a scary line about work that is safely landed.
func runPostCommit(stderr io.Writer) int {
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0
	}
	j, ok, err := tdd.PostCommitHook(root)
	if ok {
		fmt.Fprintf(stderr, "gate: mutation run started for %s (pid %d)\n", j.Branch, j.PID)
		return 0
	}
	// An early return above and a guarded statement here (rather than the
	// `switch { case ok: ...; case err != nil: ... }` this used to be) puts
	// each condition on a line with its own statement, which is what makes
	// gremlins's coverage-to-mutant mapping on the runner unambiguous -- a
	// bare `case err != nil:` line mapped to the wrong covered block and let
	// the mutant on it survive despite the killing test (issue #140).
	if err != nil {
		fmt.Fprintf(stderr, "gate: mutation run failed to start: %v\n", err)
	}
	return 0
}

// isFlagSet reports whether the caller WROTE a flag, which is not the same
// question as whether its value is empty: `--diff ""` is a CI run with a base
// nobody computed, and answering it with the local job's behaviour would run
// the wrong thing rather than say so.
func isFlagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// runningOnHostedCIRunner is the ONE explicit signal `go --diff` uses to set
// GoMutantsCI.OneJobPerContainer (issue #406): GITHUB_ACTIONS is set to
// exactly "true" by GitHub's own runner, hosted or self-hosted, for every
// job it starts — the one fact this binary can actually check, rather than
// inferring "am I in CI" from anything about the invocation itself. A
// self-hosted runner that DOES share its box with other mutation jobs is not
// this repo's problem to solve here: nothing today configures one that way,
// and the field stays a plain bool a future flag could still override.
func runningOnHostedCIRunner() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true"
}

// runGateMutants dispatches the mutation job's own verbs. They are addressed
// by a job FILE rather than by flags because the wrapper is spawned detached:
// the description of the run has to outlive the process that decided it.
func runGateMutants(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, mutantsUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stderr, mutantsUsage)
		return 0
	case "status":
		fs := flag.NewFlagSet("mutants status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		wait := fs.Bool("wait", false, "block until the run reaches a terminal state, then print it")
		if err := fs.Parse(args[1:]); err != nil {
			return tdd.ExitMutantsStatusUsage
		}
		return runMutantsStatus(".", *wait, stdout)
	case "watch":
		fs := flag.NewFlagSet("mutants watch", flag.ContinueOnError)
		fs.SetOutput(stderr)
		job := fs.String("job", "", "path to the job file to watch (default: the tree this checkout stands in)")
		if err := fs.Parse(args[1:]); err != nil {
			return tdd.ExitMutantsWatchUsage
		}
		return tdd.WatchMutantsHere(".", *job, stdout)
	case "audit":
		fs := flag.NewFlagSet("mutants audit", flag.ContinueOnError)
		fs.SetOutput(stderr)
		pkg := fs.String("package", "", "the crate (Rust) or package path (Go) to audit in full")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return tdd.RunMutantsAudit(".", *pkg, stdout, stderr)
	case "run", "go":
		fs := flag.NewFlagSet("mutants "+args[0], flag.ContinueOnError)
		fs.SetOutput(stderr)
		job := fs.String("job", "", "path to the job file describing the run")
		var diff, receipt, store *string
		var jobsFlag *int
		var baseFlag, timeoutMultiplier, minTestTimeout *string
		if args[0] == "go" {
			// CI's addressing: the merge base its diff is scoped to, and
			// where to leave the receipt for the workflow to upload. The
			// detached local job is addressed by a job FILE instead, because
			// it is spawned detached and the description of the run has to
			// outlive the process that decided it.
			diff = fs.String("diff", "", "merge base to scope the run to (CI: run in the foreground and judge)")
			receipt = fs.String("receipt", "", "where to write the signed receipt")
			// The outcome cache's directory, so a push that only changed one
			// file carries the rest of the PR's prior measurements forward
			// instead of re-measuring the whole diff (issue #143). Wired to an
			// actions/cache path keyed on the head branch; "" keeps the
			// machine-local default (the detached job's own cache).
			store = fs.String("store", "", "outcome cache directory, overriding the machine-local default (e.g. an actions/cache path keyed on the head branch)")
		} else {
			// The env-versus-flag rule: a flag typed for THIS run beats a
			// session-wide override, which beats the per-box/producer default.
			// `run` is normally spawned by postcommit, never hand-typed, but a
			// maintainer rerunning one job by hand is exactly who these are
			// for. `go` has none of this: its own concurrency comes from the
			// same per-box formula (GoMutantsCI.Workers is never set here),
			// and gremlins has neither a base override distinct from --diff
			// nor cargo-mutants' timeout knobs at all -- declaring these four
			// ONLY here (mirroring --diff/--receipt/--store's scoping to `go`
			// only) turns one mistyped on `go` into a real flag.Parse error
			// naming the flags `go` actually has, instead of a silent no-op
			// (issue #176).
			jobsFlag = fs.Int("jobs", 0, "concurrency cap for this run (default: min(cores/6, RAM/6, 2), beats "+tdd.MutantsJobsEnv+")")
			baseFlag = fs.String("base", "", "base ref/sha to scope the run to, overriding the job's own")
			timeoutMultiplier = fs.String("timeout-multiplier", "", "forwarded to cargo-mutants' own --timeout-multiplier")
			minTestTimeout = fs.String("minimum-test-timeout", "", "forwarded to cargo-mutants' own --minimum-test-timeout")
		}
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if args[0] == "go" {
			if isFlagSet(fs, "diff") {
				return tdd.RunGoMutantsCI(tdd.GoMutantsCI{
					BaseSHA: *diff, Receipt: *receipt, Store: *store,
					OneJobPerContainer: runningOnHostedCIRunner(),
				}, stderr)
			}
			// --receipt names where the CI run leaves its proof, so it means
			// nothing without --diff. Falling through here handed the detached
			// job an empty job path, which exits 0: a green check that ran
			// nothing, from a command line that asked for a run.
			if isFlagSet(fs, "receipt") {
				fmt.Fprintln(stderr, "aphrollo gate mutants go: --receipt needs --diff <base> — it is where the CI run leaves its receipt")
				return 2
			}
			// The Go half of the detached job: gremlins over the lane diff,
			// writing the same receipt the Rust runner writes.
			return tdd.RunGoMutantsJob(*job)
		}
		if isFlagSet(fs, "jobs") {
			os.Setenv(tdd.MutantsJobsEnv, strconv.Itoa(*jobsFlag))
		}
		if isFlagSet(fs, "base") {
			os.Setenv(tdd.MutantsBaseOverrideEnv, *baseFlag)
		}
		if isFlagSet(fs, "timeout-multiplier") {
			os.Setenv(tdd.MutantsTimeoutMultiplierEnv, *timeoutMultiplier)
		}
		if isFlagSet(fs, "minimum-test-timeout") {
			os.Setenv(tdd.MutantsMinTestTimeoutEnv, *minTestTimeout)
		}
		// The sensible default for an OMITTED --job: the job for the checkout
		// the caller is standing in. `run` is normally spawned by post-commit
		// with a job file, but typed by hand there is exactly one run anybody
		// means, and it used to be answered with an empty path, a failed read
		// and exit 0. An explicitly EMPTY --job stays an error: a default
		// answers a question nobody asked, never a bad answer somebody gave.
		if !isFlagSet(fs, "job") {
			return tdd.RunMutantsHere(".", stderr)
		}
		// The detached child's stdout IS the job log -- the parent redirected
		// it at spawn -- so a job that cannot be read reports there, where
		// somebody looking for the missing receipt will find it.
		return tdd.RunMutantsJobTo(*job, stdout)
	case "prove":
		fs := flag.NewFlagSet("mutants prove", flag.ContinueOnError)
		fs.SetOutput(stderr)
		file := fs.String("file", "", "the file the mutation edits")
		oldStr := fs.String("old", "", "the exact text the mutation replaces (must match exactly once)")
		newStr := fs.String("new", "", "the one specific error to introduce in its place")
		wantFail := fs.String("want-fail", "", "the test name (or a unique substring of it) the mutation is predicted to fail")
		if err := fs.Parse(args[1:]); err != nil {
			return tdd.ExitMutantsProveUsage
		}
		if *file == "" || *oldStr == "" || *newStr == "" || *wantFail == "" {
			fmt.Fprintln(stderr, "aphrollo gate mutants prove: --file, --old, --new and --want-fail are all "+
				"required — a proof names the test it expects to fail before it runs, or it is not a proof")
			return tdd.ExitMutantsProveUsage
		}
		return tdd.RunMutantsProve(tdd.MutantsProveOptions{
			File: *file, Old: *oldStr, New: *newStr, WantFail: *wantFail,
		}, tdd.RunSuite(tdd.DefaultPrecommitTimeout), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo gate mutants: unknown verb %q\n", args[0])
		fmt.Fprint(stderr, mutantsUsage)
		return 2
	}
}

// runMutantsStatus is `gate mutants status[--wait]`: it answers for the
// checkout it is standing in and never for another lane, printing the one
// line FormatMutantsStatus renders and exiting with the code that goes with
// it — the exit codes are the contract a script reads, spelled out in
// mutantsUsage below and in docs/mutation-runner.md.
func runMutantsStatus(dir string, wait bool, out io.Writer) int {
	compute := tdd.ComputeMutantsStatus
	if wait {
		compute = tdd.WaitMutantsStatus
	}
	rep, err := compute(dir)
	if err != nil {
		fmt.Fprintf(out, "aphrollo gate mutants status: %v\n", err)
		return tdd.ExitMutantsStatusError
	}
	line, code := tdd.FormatMutantsStatus(rep)
	fmt.Fprintln(out, line)
	return code
}

// mutantsUsage is what an absent, unknown or -h verb prints. `run` with no
// --job is the line a session needs and the one that did not exist: without
// it, the only thing that actually measured anything was the repo's own
// producer script invoked directly, outside the box-wide mutation lock.
const mutantsUsage = `usage: aphrollo gate mutants <verb>

  run                measure THIS checkout's lane in the foreground, under the
                     box-wide mutation lock. The base is the newest trunk
                     commit the lane already contains, which is what the merge
                     gate checks the receipt against.
  run --job <path>   measure a job file written by the post-commit hook: how a
                     detached run addresses itself, rarely typed by hand.
  go                 the Go runner's half of a detached job.
  go --diff <base>   run in the foreground and judge, for CI.
  status             answer for THIS checkout's own tree, without waiting:
                     no run started, a run going (naming the holder if it is
                     queued behind the box-wide lock rather than measuring),
                     died (exit code, log), or a receipt (verdict, counts).
                     In the normal case, do not check at all — attempt the
                     merge and read its refusal; status is for watching a run
                     or answering "why is nothing happening".
  status --wait      block on the running job's own process (never a poll
                     loop) until this tree reaches a terminal state, then
                     print the same answer.
  watch              subscribe to THIS checkout's own run: one line per state
                     transition (baseline build done, baseline test done,
                     each mutant as it is judged, each survivor as it is
                     found), blocking until a terminal state — a receipt, a
                     died run, a killed process, or one that stopped
                     advancing — and a final line naming the receipt path or
                     the reason there is none. Unlike status, terminal
                     covers every ending: a caller never has to infer an
                     outcome from silence.
  watch --job <path> watch the job a --job file describes instead of this
                     checkout's own tree, the same addressing "run --job"
                     uses.
  audit --package <name>
                     an on-demand whole-crate (Rust) or whole-package (Go)
                     run, never a diff. Reach for it when reviewing a unit,
                     not as part of merging a lane: it never writes a
                     receipt, so nothing it finds satisfies the merge gate.
                     Prints ranked survivors as "file:line: mutation".
  prove --file <path> --old <text> --new <text> --want-fail <test>
                     the HAND mutation proof (existing code, no natural RED):
                     replace --old with --new in --file — must match exactly
                     once — verify with "git diff --numstat" that the file
                     actually changed, run the file's related tests, and
                     restore the file byte-identically. Refuses rather than
                     running when the pattern matched zero or more than one
                     time, or when git sees no diff after the write: a
                     mutation that never registered proves nothing about
                     the test, whatever the run says (issue #519).

Flags for run: --jobs N, --base <ref>, --timeout-multiplier, --minimum-test-timeout.
Never invoke a repo's own mutation producer (for example tools/mutation_gate.sh)
directly: it runs outside the lock and in the wrong tree.

Exit codes for prove:
  0  killed — verified applied, the named test failed as predicted
  1  refused — the mutation never registered (bad pattern, ambiguous match,
     --old equal to --new, or an empty git diff); nothing was proved
  2  bad flags
  3  survived — verified applied, but the suite stayed green: a real survivor
  4  wrong failure — verified applied, the suite went red, but not on the
     named test
  5  timed out — the run never reached a verdict either way

Exit codes for status (and status --wait):
  0  a receipt exists and would merge (verdict pass, no unaccepted survivor, no timeout)
  1  the checkout itself could not be read (not a git repository, HEAD names no tree)
  2  bad flags
  3  no run has ever been started for this tree
  4  a run is going right now (status only; --wait never returns this)
  5  the run ended without ever writing a receipt
  6  a receipt exists but would NOT merge (bad verdict, unaccepted survivor, or timeout)

Exit codes for watch:
  0  a receipt was written
  1  the checkout itself could not be read, or the named --job file could not be
  2  bad flags
  3  no run has ever been started for this tree
  4  the run ended with a recorded exit code and no receipt
  5  the run's process is gone with no recorded exit and no receipt (killed)
  6  the run's process is alive but stopped advancing (stalled)
`
