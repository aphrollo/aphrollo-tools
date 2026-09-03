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
	dir, err := os.MkdirTemp("", "aphrollo-mutants-ci-")
	if err != nil {
		logf(out, "aphrollo: %v", err)
		return 1
	}
	defer os.RemoveAll(dir)

	workers := c.Workers
	if workers < 1 {
		workers, _ = mutantsJobsForThisBox()
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

	logf(out, "aphrollo: %d mutant(s) over %s..HEAD — %d caught, %d timed out, %d unviable, %d survived (%d accepted)",
		r.MutantsTotal, short(c.BaseSHA), r.Caught, r.Timeout, r.Unviable, len(r.Survivors), r.Accepted)
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
