package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runPostCommit is the `gate postcommit` git hook. It writes the
// refs/notes/gate note on the commit just made — what lets CI tell a red on a
// gated tip from a red on an ungated one — and nothing else. It NEVER blocks
// and never reports failure: the commit has already happened by the time this
// runs, so a non-zero exit would only print a scary line about work that is
// safely landed.
//
// It used to start a detached mutation run here as well. That run held the
// box-wide lock for hours (gate.log recorded interactive build-slot waits of
// 26,460 s, 22,440 s and 14,760 s behind a single one), failed to create its
// worktree 27 times in 14 days, and produced a document a later merge judged
// instead of a measurement. The measurement now happens in the foreground, on
// the tree being merged.
func runPostCommit(stderr io.Writer) int {
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0
	}
	tdd.PostCommit(root)
	return 0
}

// runGateMutants dispatches the mutation verbs. There are two: measure this
// checkout against its base, and prove one hand-written mutation. The verbs
// that addressed, watched or judged a DETACHED run are gone with the run
// itself — status, watch, audit, `run --job` and the whole `go` half existed
// to talk to a process that no longer exists.
func runGateMutants(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, mutantsUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stderr, mutantsUsage)
		return 0
	case "run":
		return runGateMutantsRun(args[1:], stdout, stderr)
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

// runGateMutantsRun is `gate mutants run`: read what the repo declares, measure
// this checkout's lane against its base in the foreground, print the report on
// STDOUT and the run's own narrative on stderr. Exit 1 when the verdict refuses
// — the whole command exists to be a check something can run.
//
// The narrative and the verdict go to different streams on purpose: the report
// leads with the surviving mutant, and a caller reading the first line of
// stdout must get that mutant rather than whatever the tool happened to print
// while it worked.
func runGateMutantsRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mutants run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base ref or sha to measure against (default: the merge base with the default branch)")
	// There is no --jobs: a Cargo measurement is in-place and cargo-mutants
	// refuses --jobs beside --in-place, so a flag taking a number here would
	// promise concurrency the tool will not give (issue #592). An unknown
	// flag is refused by the flag package's own message rather than ignored.
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := tdd.RepoRoot(".")
	if root == "" {
		fmt.Fprintln(stderr, "aphrollo gate mutants run: not a git repository, so there is no lane to measure")
		return 1
	}
	cfg, err := tdd.ReadMutantsConfig(root)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants run: %v\n", err)
		return 1
	}
	v, err := tdd.MeasureLane(root, cfg, tdd.MeasureOpts{Base: *base, Log: stderr})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants run: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, v.Message)
	if v.Refused {
		return 1
	}
	return 0
}

// mutantsUsage is what an absent, unknown or -h verb prints.
const mutantsUsage = `usage: aphrollo gate mutants <verb>

  run                measure THIS checkout's lane in the foreground, under the
                     box-wide mutation lock, and print the report: every
                     unaccepted surviving mutant first, then the counts, then
                     the remedy. Exit 1 when a mutant survived unaccepted, when
                     one timed out twice, or when the run reached no verdict at
                     all. The base is the newest trunk commit the lane already
                     contains — the same diff the merge will measure.
  run --base <ref>   measure against that ref or sha instead. What nightly CI
                     on main passes its checkpoint to.

                     There is no --jobs: a Cargo run measures the tree
                     in place, and cargo-mutants refuses --jobs beside
                     --in-place, so the run is one job by construction.
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

Exit codes for run:
  0  every mutant the lane's diff generated was caught, accepted or skipped
  1  a mutant survived unaccepted, one stayed unmeasured, the run reached no
     verdict, or the repo's own configuration was refused
  2  bad flags

Exit codes for prove:
  0  killed — verified applied, the named test failed as predicted
  1  refused — the mutation never registered (bad pattern, ambiguous match,
     --old equal to --new, or an empty git diff); nothing was proved
  2  bad flags
  3  survived — verified applied, but the suite stayed green: a real survivor
  4  wrong failure — verified applied, the suite went red, but not on the
     named test
  5  timed out — the run never reached a verdict either way
`
