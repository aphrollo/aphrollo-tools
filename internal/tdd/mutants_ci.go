package tdd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The Go runner has two callers. The LOCAL one is detached: the post-commit
// hook starts it, it reports, and the merge gate judges what it wrote. The CI
// one is the JUDGE — it runs in the foreground on a pull request, and an
// unaccepted survivor is a red check rather than a line in a file somebody
// may read.
//
// Which box measures is the whole reason for the split. A Go mutant is judged
// by re-running its WHOLE package: 26 s for internal/tdd on the Linux runner
// against 207 s on a Windows developer box, and gremlins' analysis pass counts
// 1626 mutants in that one package. A repo with a runner says
// `mutants-local = false` and the proof moves off the box that is trying to
// edit code.

// GoMutantsCI is one CI run: measure the pull request's own diff in this
// checkout and judge the survivors against the repo's accept-list.
type GoMutantsCI struct {
	// Root is the checkout to measure; "" means the current directory.
	Root string
	// BaseSHA is the merge base the diff is scoped to. There is no default:
	// an unscoped run measures the whole module.
	BaseSHA string
	// Receipt is where the signed receipt is written for the workflow to
	// upload. Empty writes it to the machine's own receipt store.
	Receipt string
	// Workers caps the parallel mutant runs; 0 asks the box.
	Workers int
}

// goMutantsRunFn is the tool, as a seam: a test proves the judging without a
// mutation run, which is the one thing that cannot be made cheap.
var goMutantsRunFn = runGremlinsAt

// mutantsJobsFn is the box's own cap, as a seam. The cap is 1 or 2 on every
// real machine, so a test pinning the `workers < 1` boundary cannot tell "the
// caller asked for 1" from "the box answered 1" unless it chooses the box's
// answer itself. A CI runner answering 1 is exactly where that survived.
var mutantsJobsFn = mutantsJobsForThisBox

// runGremlinsAt is one diff-scoped run in a checkout that is already there —
// the CI half's equivalent of runGremlins, which runs in the warm worktree a
// detached job set up.
func runGremlinsAt(root, baseSHA, outPath string, workers int) int {
	return runCommandIn(root, gremlinsBin, gremlinsArgv(baseSHA, outPath, workers))
}

// RunGoMutantsCI measures the diff and judges it, returning the process exit
// code: 0 when every survivor is accepted, 1 when one is not or the run
// produced nothing to judge, 2 when the invocation itself is wrong.
//
// gremlins' OWN exit code is not the verdict. It fails a run that misses its
// efficacy threshold, which is a bar about the whole module; the bar here is
// the repo's accept-list, and a report this runner can read is judged on what
// it says.
func RunGoMutantsCI(c GoMutantsCI, out io.Writer) int {
	if strings.TrimSpace(c.BaseSHA) == "" {
		logf(out, "aphrollo: gate mutants go --diff needs the merge base to scope the run")
		return 2
	}
	root := c.Root
	if root == "" {
		root = "."
	}
	if r := RepoRoot(root); r != "" {
		root = r
	}

	// Everything the receipt needs to name the run, taken before the run so
	// both exits below write the same document.
	writeReceiptFor := func(mutants []MutantOutcome) MutationReceipt {
		r := goMutantsReceipt(goMutantsRun{
			Repo:     commonGitDir(root),
			Branch:   gitOut(root, "rev-parse", "--abbrev-ref", "HEAD"),
			TipTree:  gitOut(root, "rev-parse", "HEAD:"),
			BaseRef:  c.BaseSHA,
			BaseSHA:  c.BaseSHA,
			Worktree: root,
		}, mutants, treeStateAt(root, "HEAD"))
		path := c.Receipt
		if path == "" {
			path = MutationReceiptPathFor(r.TipTree)
		}
		if path != "" {
			writeReceiptFile(path, r)
		}
		return r
	}

	// A zero-mutant run is a real answer exactly when there was nothing to
	// mutate. gremlins mutates PRODUCTION Go, so a diff of YAML, markdown,
	// tests and fixtures measures zero because that is the truth about the
	// lane — while a zero over a diff that DID change production Go means the
	// scope matched nothing, which is what a stale base looks like. The two
	// are indistinguishable from the report alone, so the diff is listed
	// first and the tool is not even started when there is nothing in it.
	if !diffHasMutableGo(root, c.BaseSHA) {
		writeReceiptFor(nil)
		logf(out, "aphrollo: 0 mutable Go lines in %s..HEAD: nothing to judge", c.BaseSHA)
		return 0
	}

	dir, err := os.MkdirTemp("", "aphrollo-mutants-ci-")
	if err != nil {
		logf(out, "aphrollo: %v", err)
		return 1
	}
	defer os.RemoveAll(dir)

	workers := c.Workers
	if workers < 1 {
		workers, _ = mutantsJobsFn()
	}
	report := filepath.Join(dir, "gremlins.json")
	code := goMutantsRunFn(root, c.BaseSHA, report, workers)

	data, err := os.ReadFile(report)
	if err != nil {
		logf(out, "aphrollo: gremlins wrote no report (exit %d): %v", code, err)
		return 1
	}
	mutants, err := parseGremlinsReport(data)
	if err != nil {
		logf(out, "aphrollo: unreadable gremlins report: %v", err)
		return 1
	}

	r := writeReceiptFor(mutants)

	logf(out, "aphrollo: %d mutant(s) over %s..HEAD — %d caught, %d timed out, %d unviable, %d survived (%d accepted)",
		r.MutantsTotal, short(c.BaseSHA), r.Caught, r.Timeout, r.Unviable, len(r.Survivors), r.Accepted)

	// Three ways to fail, and none of them is "a survivor" alone.
	//
	// A run that measured NOTHING is the first: gremlins marks everything
	// outside its --diff scope SKIPPED, so a stale or wrong base produces a
	// clean-looking zero. That is the same hole the path argument had — walk
	// nothing, report nothing, exit 0 — re-entering through the base. The
	// MERGE gate accepts a zero-mutant receipt, because a diff with nothing
	// mutable in it is a real answer about a lane; the run is the thing that
	// could have been mis-scoped, and only the run can tell.
	if r.MutantsTotal == 0 {
		logf(out, "aphrollo: the run measured NO mutants over %s..HEAD — a scope that matches nothing"+
			" is not a proof: check that the base is the merge base this branch actually diverged from", c.BaseSHA)
		return 1
	}
	// A timeout is an unmeasured mutant filed beside the measured ones, and
	// with the local run off this check is the only judge the repo has.
	if r.Timeout > 0 {
		logf(out, "aphrollo: %s", mutantsTimedOutLine(r.Timeout))
		return 1
	}
	if len(r.Unaccepted) == 0 {
		return 0
	}
	logf(out, "aphrollo: %d survivor(s) nobody accepted — give each a test, or an aphrollo.toml"+
		" [aphrollo] mutation-accept entry that states why it is acceptable:", len(r.Unaccepted))
	for _, name := range r.Unaccepted {
		logf(out, "  %s", name)
	}
	return 1
}

// diffHasMutableGo reports whether base..HEAD changes a file a mutation run
// could mutate at all. A diff git cannot read answers YES: "I could not tell"
// must never be the reason a check passes, and the zero-mutant refusal below
// is then the one that speaks.
func diffHasMutableGo(root, baseSHA string) bool {
	// --diff-filter=d drops deletions: a file that is gone has nothing to
	// mutate, and a lane that only deletes production Go legitimately
	// measures zero.
	out, err := git(root, "diff", "--name-only", "--diff-filter=d", baseSHA, "HEAD")
	if err != nil {
		return true
	}
	for line := range strings.Lines(out) {
		if isMutableGoPath(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// isMutableGoPath is the one definition of "production Go" this check uses: a
// .go file that is not a test and does not live in a testdata tree. gremlins
// mutates nothing else, so nothing else can make a zero suspicious.
func isMutableGoPath(p string) bool {
	p = filepath.ToSlash(p)
	if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "testdata" {
			return false
		}
	}
	return true
}
