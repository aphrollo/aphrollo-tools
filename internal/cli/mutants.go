package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/commitrecord"
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
//
// postCommitRoutineSeam is called once, every time this actually reaches
// tdd.PostCommit — the same proof-of-non-execution premergeRoutineSeam gives
// the merge gate, so a test can show a help flag never wrote the note.
var postCommitRoutineSeam = func() {}

func runPostCommit(stdout, stderr io.Writer) int {
	postCommitRoutineSeam()
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0
	}
	// First, and cheap (one git call, one append): the canary's record of every
	// commit made through this path. It fails open.
	commitrecord.Record(root)
	tdd.PostCommit(root)
	// The guarded lane sweep's second path (issue #716): a lane landed by
	// resolving a conflict and concluding with a plain `git commit` never
	// fires git's own post-merge hook (runPostMerge) at all, so it is caught
	// here instead — opt-in on the same key, and inert for the ordinary
	// single-parent commit this hook fires on constantly.
	tdd.PostCommitMergeSweep(".", stdout, stderr)
	return 0
}

// runPostMerge is the `gate postmerge` git hook: the opt-in lane sweep for
// the repo the merge landed in, run from the worktree git fired the hook in
// (which is therefore the one worktree the sweep must never remove). Like
// post-commit it never blocks and never reports failure — the merge is
// already made — and unlike it, it does nothing at all unless the repo
// declared `prune-lanes-on-merge = true`: core.hooksPath is machine-wide, so
// this fires in every repo on the box and after every `git pull`, and the
// sweep removes worktrees and deletes branches.
func runPostMerge(stdout, stderr io.Writer) int {
	tdd.PostMergeSweep(".", stdout, stderr)
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
	case "verdict":
		return runGateMutantsVerdict(args[1:], stdout, stderr)
	case "hold":
		return runGateMutantsHold(args[1:], stdout, stderr)
	case "commit":
		return runGateMutantsCommit(stdout, stderr)
	case "testmap":
		return runGateMutantsTestMap(args[1:], stdout, stderr)
	case "edit":
		return runGateMutantsEdit(args[1:], stderr)
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
		noteMeasuredInCIAt(".", stderr)
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

// runGateMutantsCommit is `gate mutants commit`: the commit gate's own
// mutation stage, run by hand on the staged change of this checkout. Exit 1
// when a commit would be refused.
func runGateMutantsCommit(stdout, stderr io.Writer) int {
	root := tdd.RepoRoot(".")
	if root == "" {
		fmt.Fprintln(stderr, "aphrollo gate mutants commit: not a git repository, so there is no staged change to measure")
		return 1
	}
	return tdd.RunMutantsCommit(root, stdout, stderr)
}

// runGateMutantsEdit is `gate mutants edit --file <path> --done <path>`: the
// commit stage over the lines one edit changed against HEAD, which the edit
// hook starts detached with stderr in a log and reads at a later hook. The
// result is recorded in --done, written last, so the hook never waits on a run
// that reached no verdict; outside a repository it records "ok".
func runGateMutantsEdit(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("mutants edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "the edited file")
	done := fs.String("done", "", "where to record how the run ended")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *done == "" {
		fmt.Fprintln(stderr, "aphrollo gate mutants edit: --file and --done are both required")
		return 2
	}
	root := tdd.RepoRoot(".")
	if root == "" {
		if err := os.WriteFile(*done, []byte("ok\n"), 0o600); err != nil {
			fmt.Fprintf(stderr, "aphrollo gate mutants edit: recording the result: %v\n", err)
			return 1
		}
		return 0
	}
	return tdd.RunMutantsEdit(root, *file, *done, stderr)
}

// runGateMutantsTestMap is `gate mutants testmap`: build the per-function test
// maps the commit-time run selects tests with. It is what the post-merge hook
// starts in the background, so outside a repo, or in one that declared no
// mutants-at-commit, it does nothing and says nothing.
func runGateMutantsTestMap(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mutants testmap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var pkgs stringList
	fs.Var(&pkgs, "pkg", "a package directory to build the map of (repeatable); default: every package with tests")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0
	}
	return tdd.RunMutantsTestMap(root, pkgs, stdout, stderr)
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
	// Where a run measuring on behalf of another box publishes what it
	// measured, bound to the tree it measured (internal/tdd/mutation/mutants_runner.go).
	// Empty writes nothing, which is every run a developer types.
	report := fs.String("report", "", "also write this run's per-mutant outcomes here, bound to the tree they were measured on")
	// One slice of a lane's measurement divided across CI runners: this run
	// measures the changed files it owns, writes them to --report and judges
	// nothing; `gate mutants verdict` judges the merged slices.
	shardSpec := fs.String("shard", "", "measure only shard <index>/<count> of the changed files and write it to --report, judging nothing")
	// There is no --jobs: a Cargo measurement is N cargo-mutants processes,
	// one per shard, each with --jobs 1, and N is derived from the box that
	// has to hold their builds rather than typed by a caller (issue #592).
	// An unknown flag is refused by the flag package's own message rather
	// than ignored.
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
	// A run that ends leaves its temp copies and scratch behind whether or not
	// it reached a verdict, so the sweep follows every exit below.
	defer sweepAfterRun(root)
	opts := tdd.MeasureOpts{Base: *base, Log: stderr, ReportOut: *report}
	if *shardSpec != "" {
		if opts.Shard, opts.Shards, err = tdd.ParseShardSpec(*shardSpec); err != nil {
			fmt.Fprintf(stderr, "aphrollo gate mutants run: %v\n", err)
			return 2
		}
	}
	noteMeasuredInCI(cfg, stderr)
	v, err := tdd.MeasureLane(root, cfg, opts)
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

// runGateMutantsVerdict is `gate mutants verdict`: judge the reports of the
// shards of one measurement as a single run, on the tree checked out here.
// It prints the report on stdout like `run`, and exits 1 when the merged
// run is refused or the reports are not the whole measurement.
func runGateMutantsVerdict(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mutants verdict", flag.ContinueOnError)
	fs.SetOutput(stderr)
	shards := fs.Int("shards", 0, "how many shard reports to expect")
	report := fs.String("report", "", "also write the merged outcomes here, bound to the tree they were measured on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *shards < 1 || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "aphrollo gate mutants verdict: --shards and at least one shard report are required")
		return 2
	}
	root := tdd.RepoRoot(".")
	if root == "" {
		fmt.Fprintln(stderr, "aphrollo gate mutants verdict: not a git repository, so there is no tree to judge")
		return 1
	}
	cfg, err := tdd.ReadMutantsConfig(root)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants verdict: %v\n", err)
		return 1
	}
	v, err := tdd.JudgeShardReports(root, cfg, fs.Args(), *shards, *report, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants verdict: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, v.Message)
	if v.Refused {
		return 1
	}
	return 0
}

// noteMeasuredInCI says, on a hand run, that the repo's measurement is CI's:
// the gate never runs it locally, so what a hand run finds is the caller's
// own. CI itself runs these verbs and needs no reminder.
func noteMeasuredInCI(cfg tdd.MutantsConfig, stderr io.Writer) {
	if (cfg.AtMergeCI || cfg.BeforePRCI) && os.Getenv("GITHUB_ACTIONS") != "true" {
		fmt.Fprintln(stderr, "mutants: this repo measures in CI (mutants-verdict); a hand run here is your own check, not the gate's")
	}
}

// noteMeasuredInCIAt is noteMeasuredInCI for the repo holding dir, when it
// declares anything readable.
func noteMeasuredInCIAt(dir string, stderr io.Writer) {
	root := tdd.RepoRoot(dir)
	if root == "" {
		return
	}
	if cfg, err := tdd.ReadMutantsConfig(root); err == nil {
		noteMeasuredInCI(cfg, stderr)
	}
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
  run --report <p>   also write this run's per-mutant outcomes to <p>, bound to
                     the git tree id of the tree they were measured on. How the
                     self-hosted Linux runner measures the Go half on behalf of
                     a Windows box, where gremlins reads 0.00% mutator coverage
                     and no local run can say anything: the pre-merge gate there
                     consumes a report only when its tree id is the tree the
                     gate is judging, and reports NOT MEASURED otherwise.

                     There is no --jobs: a Cargo run is one cargo-mutants
                     process per shard of the mutant pool, each with
                     --jobs 1, and the shard count comes from the box.
  run --shard <i>/<n> --report <p>
                     measure only the changed source files shard i of n owns
                     (0-based, weighted by added lines) and write the outcomes
                     to <p>; nothing is judged. How CI divides one measurement
                     across hosted runners.
  verdict --shards <n> [--report <p>] <report>...
                     judge the n shard reports as one run, against the accept-list
                     of the tree checked out here: refused when a report is
                     missing, twice present, or of another tree. Exit codes as run.
  commit             the commit gate's mutation stage, by hand, on the staged
                     change (repos that declare mutants-at-commit = true): mutate
                     only the lines the change adds, run each mutant against the
                     tests selected for its function, and name each survivor.
                     Exit 1 when a commit would be refused.
  edit --file <path> --done <path>
                     the same over the lines one edit changed against HEAD, in the
                     working tree as it stands. The edit hook starts it detached and
                     reads the result at the next hook; <done> records "ok" or
                     "refused", written last. Exit 1 when it refused.
  testmap [--pkg <dir>]...
                     build the per-function test maps that selection uses, for
                     the named packages or every package with tests, skipping
                     the ones already current. The post-merge hook starts it in
                     the background; silent where mutants-at-commit is not set.
  hold [--dry] <file>...
                     take the pre-mutation WORKING state of each file, for a
                     hand proof run by editor rather than by "prove" (--dry
                     prints the file it would hold and holds nothing). The
                     restore afterwards is "MUTATION=1 git checkout -- <file>":
                     the git shim serves it from the held bytes, so it puts
                     back what the proof started from — including any
                     uncommitted work in the file, and including an untracked
                     file — rather than what the index holds. The hold is
                     scoped to this session and expires after 2h.
  prove --file <path> --old <text> --new <text> --want-fail <test>
                     the HAND mutation proof (existing code, no natural RED):
                     copy the lane as it stands (staged, uncommitted and
                     untracked work included) beside it, replace --old with
                     --new in the copy's --file — must match exactly once —
                     verify that the file's content (as git reads it)
                     changed from the bytes the proof started with, run the
                     file's related tests scoped to --want-fail in the copy
                     with fail-fast off (widened when that selects none), and
                     remove the copy. The lane itself is never written, so a
                     mutant that writes or resets the directory it runs in
                     reaches only the copy. Refuses rather than
                     running when the pattern matched zero or more than one
                     time, or when the content is unchanged after the write: a
                     mutation that never registered proves nothing about
                     the test, whatever the run says (issue #519).

Exit codes for run:
  0  every mutant the lane's diff generated was caught, accepted or skipped
  1  a mutant survived unaccepted, one stayed unmeasured, one not covered or
     inconclusive sits on a line the lane adds (with mutants-at-merge on), the
     run reached no verdict, the coverage run failed on the lane's own tree (a
     build error or a failing test, named by package), or the repo's own
     configuration was refused. A coverage run the box broke (a killed
     process, a full drive, no memory) is NOT MEASURED and exits 0
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
  6  unreadable — verified applied, the suite went red, but not one failing
     test name could be read out of the run
  7  no tests selected — verified applied, the run finished, but its filter
     selected zero tests: nothing exercised the mutation, so this is a
     refusal and never a survivor
  8  scope unknown — verified applied, the narrowed run stayed green, and
     which OTHER packages' tests can reach the mutated one could not be
     established: inconclusive, and never a survivor
`
