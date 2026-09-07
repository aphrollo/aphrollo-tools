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
	// Store is the outcome cache's directory, overriding the machine-local
	// mutantsStateDir() default — CI's `--store <dir>`, wired to a directory
	// actions/cache restores and saves keyed on the head branch, so a second
	// push on the SAME pull request carries the first push's measurements
	// forward instead of re-running the whole diff (issue #143). "" keeps the
	// machine-local default (the detached local job's own cache).
	Store string
	// OneJobPerContainer says this run may assume nothing else on the
	// machine is measuring mutants at the same time — true on a hosted
	// GitHub Actions runner, which never shares its container with a second
	// job. RunGoMutantsCI takes the box-wide mutation-run lock UNLESS this
	// is set, because the zero value is the case the lock exists to protect:
	// a developer typing `aphrollo gate mutants go --diff` by hand on a box
	// that may already be running a detached local job (issue #406), the
	// same oversubscription issue #253 describes for two local jobs. A
	// hosted runner gains nothing from the lock — it is the only job on the
	// machine — and only risks blocking on a stale lock file left by
	// something else, so it is the one caller that sets this explicitly,
	// from the one signal that actually says so (GITHUB_ACTIONS), rather
	// than this type inferring it from anything about the run itself.
	OneJobPerContainer bool
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
// detached job set up. excludeFiles narrows what gremlins walks at all (see
// gremlinsArgv).
func runGremlinsAt(root, baseSHA, outPath string, workers int, excludeFiles []string) int {
	return runCommandIn(root, gremlinsBin, gremlinsArgv(baseSHA, outPath, workers, excludeFiles))
}

// ciMutantStorePath resolves the CI run's outcome cache: explicit under
// store when the caller named one (CI's `--store <dir>`), the machine-local
// default otherwise. Unlike MutantStorePath, an explicit store is NOT further
// namespaced by repo — the caller's directory (an actions/cache path keyed on
// the head branch) already scopes it to one pull request.
func ciMutantStorePath(store, repo string) string {
	if store == "" {
		return MutantStorePath(repo)
	}
	if err := os.MkdirAll(store, 0o700); err != nil {
		return ""
	}
	return filepath.Join(store, "outcomes.json")
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

	// movedLines is filled in once the plan below has taken its move-aware
	// pass; the closure reads it at CALL time, so every write after that pass
	// carries the real count.
	var movedLines int

	// Everything the receipt needs to name the run, taken before the run so
	// both exits below write the same document.
	writeReceiptFor := func(mutants []MutantOutcome) MutationReceipt {
		r := goMutantsReceipt(goMutantsRun{
			Repo:       commonGitDir(root),
			RepoID:     repoIdentity(root),
			Branch:     gitOut(root, "rev-parse", "--abbrev-ref", "HEAD"),
			TipTree:    gitOut(root, "rev-parse", "HEAD:"),
			BaseRef:    c.BaseSHA,
			BaseSHA:    c.BaseSHA,
			Worktree:   root,
			MovedLines: movedLines,
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

	// The incremental plan: which of the lane's files still need a fresh
	// gremlins measurement, and which outcomes the store already answers for
	// (issue #143). lane is the WHOLE diff (every file, not just mutable Go —
	// PlanDiffFiles classifies), so a file gremlins could never mutate is
	// never counted as "carried" either.
	storePath := ciMutantStorePath(c.Store, commonGitDir(root))
	cached := loadMutantStoreAt(storePath)
	lane, laneOK := changedPaths(root, c.BaseSHA, "HEAD")
	now := treeStateAt(root, "HEAD")
	producerVersion := mutantsProducerVersion(root)
	invocationVersionFor := mutantsInvocationVersionFor(root, producerVersion)
	files := lane
	var carried []MutantOutcome
	if laneOK {
		files = PlanDiffFiles(root, lane, now, cached, producerVersion, invocationVersionFor)
		carried = PlanMutants(laneWants(cached, lane), now, cached, producerVersion, invocationVersionFor).Carry
	}

	// Move-aware, on top of the store's own plan: a file left in `files`
	// because the store had never measured it may still have nothing worth
	// mutating, when everything base..HEAD changed in it was git-detected as
	// moved rather than edited. Excluding those here is what keeps a
	// crate-topology-style relocation of Go code from re-mutating lines
	// nobody touched.
	var movedOnly []string
	if laneOK && len(files) > 0 {
		movedOnly, movedLines = movedOnlyFiles(root, c.BaseSHA, "HEAD", files)
		if len(movedOnly) > 0 {
			moved := make(map[string]bool, len(movedOnly))
			for _, f := range movedOnly {
				moved[f] = true
			}
			kept := files[:0]
			for _, f := range files {
				if !moved[f] {
					kept = append(kept, f)
				}
			}
			files = kept
		}
	}

	if laneOK && len(files) == 0 {
		// Every file in the lane is either already answered by the store at
		// its current blob and fence, or — when movedLines is what accounts
		// for the rest — never had anything but relocated code in it. Either
		// way gremlins is not even started.
		r := writeReceiptFor(carried)
		reason := "nothing changed since the last measured push"
		if movedLines > 0 {
			reason = "every remaining changed line was a pure move"
		}
		logf(out, "aphrollo: 0 measured, %d carried over %s..HEAD — %s",
			len(carried), short(c.BaseSHA), reason)
		return judgeGoMutantsCI(r, 0, c.BaseSHA, out)
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
	// excludeFiles is the lane's files the plan already answered for — kept
	// OUT of this run's walk (gremlinsArgv), so a push that touches one file
	// costs that file's package, not the whole lane.
	excludeFiles := exceptFiles(lane, files)
	report := filepath.Join(dir, "gremlins.json")
	if !c.OneJobPerContainer {
		// The same box-wide lock the local Go job path takes (mutants_go.go,
		// issue #253's own fix): gremlins re-runs the whole package's test
		// suite per mutant, the identical wall-clock-threads resource a
		// second concurrent run oversubscribes. This path never took it at
		// all until issue #406 — a developer running `mutants-ci` by hand
		// on a box already running a detached local job did exactly that.
		releaseRunLock := acquireMutantsRunLock("mutants-ci for "+commonGitDir(root), root)
		defer releaseRunLock()
	}
	code := goMutantsRunFn(root, c.BaseSHA, report, workers, excludeFiles)

	data, err := os.ReadFile(report)
	if err != nil {
		logf(out, "aphrollo: gremlins wrote no report (exit %d): %v", code, err)
		return 1
	}
	fresh, err := parseGremlinsReport(data)
	if err != nil {
		logf(out, "aphrollo: unreadable gremlins report: %v", err)
		return 1
	}
	// Stamped with the CURRENT blob, fence and producer version before it
	// reaches the store: mergeMutantStoreAt drops an entry carrying no blob or
	// fence, on purpose (an unmeasurable entry can never be shown to still
	// hold), and a raw gremlins outcome carries none of the three until
	// something stamps it.
	fresh = stampTreeState(fresh, now, producerVersion, invocationVersionFor)
	mergeMutantStoreAt(storePath, fresh)

	// De-duplicated by mutant key, the same guard adoptCarriedOutcomes
	// applies on the job path: a file can land in both `fresh` and `carried`
	// for the same tip when its cached ProducerVersion put it back into the
	// re-measure set — fresh, the just-measured answer, always wins.
	carried = dedupByMutantKey(fresh, carried)
	r := writeReceiptFor(append(append([]MutantOutcome{}, fresh...), carried...))

	logf(out, "aphrollo: %d measured, %d carried over %s..HEAD — %d caught, %d timed out, %d unviable, %d survived (%d accepted)",
		len(fresh), len(carried), short(c.BaseSHA), r.Caught, r.Timeout, r.Unviable, len(r.Survivors), r.Accepted)

	return judgeGoMutantsCI(r, len(fresh), c.BaseSHA, out)
}

// stampTreeState fills in each mutant's package, blob, fence, producer
// version and invocation version from now, producerVersion and
// invocationVersionFor — everything that decides whether a later run may
// carry it forward instead of re-measuring it (mutants_plan.go,
// mutants_treestate.go, mutants_invocation_version.go). Package is resolved
// FIRST and invocationVersionFor read against IT, never a value fixed before
// the loop, so a future per-package split (issue #531) needs no change here.
func stampTreeState(mutants []MutantOutcome, now TreeState, producerVersion string, invocationVersionFor InvocationVersionFor) []MutantOutcome {
	out := make([]MutantOutcome, len(mutants))
	for i, m := range mutants {
		m.Package = now.Packages[m.File]
		m.Blob, m.Fence = now.Blobs[m.File], now.Fences[m.Package]
		m.ProducerVersion = producerVersion
		m.InvocationVersion = invocationVersionFor(m.Package)
		out[i] = m
	}
	return out
}

// exceptFiles is lane minus files, the lane's own files the plan already
// carries — what gremlinsArgv must exclude from this run's walk.
func exceptFiles(lane, files []string) []string {
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f] = true
	}
	var out []string
	for _, p := range lane {
		if !keep[p] {
			out = append(out, p)
		}
	}
	return out
}

// judgeGoMutantsCI is the three ways a run can fail, and none of them is "a
// survivor" alone. measured is how many mutants THIS run actually measured
// (excluding carried ones): a run whose lane needed fresh measurement but
// produced none of it is the same stale-base hole a zero-mutant run always
// was, whether or not the receipt's total is padded by carried outcomes.
//
// A run that measured NOTHING is the first: gremlins marks everything
// outside its --diff scope SKIPPED, so a stale or wrong base produces a
// clean-looking zero. That is the same hole the path argument had — walk
// nothing, report nothing, exit 0 — re-entering through the base. The MERGE
// gate accepts a zero-mutant receipt, because a diff with nothing mutable in
// it is a real answer about a lane; the run is the thing that could have
// been mis-scoped, and only the run can tell.
//
// r.MovedLines > 0 is the one zero this ambiguity check must not catch: it
// means the plan itself (never gremlins) decided there was nothing left to
// measure, because git's own move detection accounted for every line the
// lane's remaining files changed — a real, explained answer, not a scope
// that matched nothing.
func judgeGoMutantsCI(r MutationReceipt, measured int, baseSHA string, out io.Writer) int {
	if measured == 0 && vacuousMutationRun(r.MutantsTotal, r.MovedLines) {
		logf(out, "aphrollo: the run measured NO mutants over %s..HEAD — a scope that matches nothing"+
			" is not a proof: check that the base is the merge base this branch actually diverged from", baseSHA)
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
