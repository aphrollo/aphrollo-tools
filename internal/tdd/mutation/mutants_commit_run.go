package mutation

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The commit-time runner. It executes each mutant the way `gate mutants
// prove` and the settle run do — swapped in through `go test -overlay` inside
// a disposable copy of the lane, so the checkout is never written — but runs
// only the tests selected for the mutant's function (mutants_testmap.go), on
// a bounded number of workers, inside one wall-clock budget. Whatever the
// budget does not reach is NOT MEASURED, never a survivor and never a pass.

// commitPlan is what the runner knows about one package's tests: the map
// (nil when the cache is cold), the tests the package has now, the ones the
// commit touched, and whether a changed TestMain makes every selection
// unsafe.
type commitPlan struct {
	Dir     string
	Map     *testMap
	Current []string
	Touched []string
	Whole   bool
}

// commitRun is what happened to one mutant. Outcome.Status is caught, missed
// or unviable when it was measured, and NotMeasured says why it was not
// otherwise. Selected is the tests it was first run against, and
// WholePackage says the whole package's tests were run for it.
type commitRun struct {
	Mutant       commitMutant
	Outcome      MutantOutcome
	NotMeasured  string
	GapKind      string
	Selected     []string
	WholePackage bool
	// Took is how long the mutant's own runs took, measured or not.
	Took time.Duration
	// pending says the cheap run did not settle the mutant: it waits for its
	// run against the whole package.
	pending bool
}

// The kinds of gap a mutant can be left in, counted separately in the gate
// log: the wall-clock ran out, the run could not be set up or started, or a
// failure was not shown to be the mutant's kill.
const (
	gapBudget      = "budget"
	gapRunner      = "runner"
	gapUnconfirmed = "unconfirmed"
)

// forEachWorker calls do(worker, index) for every index, on at most workers
// goroutines, each identified by its own number, and returns when all are
// done.
func forEachWorker(workers int, indexes []int, do func(worker, index int)) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := range min(workers, len(indexes)) {
		wg.Go(func() {
			for i := range jobs {
				do(w, i)
			}
		})
	}
	for _, i := range indexes {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// commitGap is why a mutant was not judged. The zero value is no gap.
type commitGap struct{ Kind, Why string }

// leave records a gap in run and answers whether there was one.
func (r *commitRun) leave(gap commitGap) bool {
	r.NotMeasured, r.GapKind = gap.Why, gap.Kind
	return gap.Why != ""
}

// runCommitMutants runs every mutant and answers one commitRun each, in the
// order given. At most workers of them run at once, and nothing is started
// after budget has passed since the call began.
func runCommitMutants(ctx context.Context, root string, cfg MutantsConfig, plans map[string]*commitPlan,
	mutants []commitMutant, workers int, budget time.Duration, log io.Writer) []commitRun {
	runs := make([]commitRun, len(mutants))
	if len(mutants) == 0 {
		return runs
	}
	deadline := time.Now().Add(budget)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	workers = min(max(workers, 1), len(mutants))
	ctx = withCapShare(ctx, workers)
	env := measureEnv(root, cfg)
	work := filepath.Join(measureTempDir(root), "commit")
	defer func() { _ = os.RemoveAll(work) }()

	boxes := make([]*commitBox, workers)
	for w := range boxes {
		boxes[w] = &commitBox{root: root, worker: w}
		defer boxes[w].close()
	}
	all := make([]int, len(mutants))
	for i, m := range mutants {
		all[i] = i
		runs[i] = commitRun{Mutant: m, Outcome: MutantOutcome{
			File: m.File, Line: m.Line, Col: m.Col, Mutation: m.Mutation,
			Name: mutantLineOf(m.File, m.Line, m.Col, m.Mutation),
		}}
	}
	known := &killChecks{}
	step := func(confirm bool) func(w, i int) {
		return func(w, i int) {
			r := &runs[i]
			if !time.Now().Before(deadline) {
				r.leave(commitGap{gapBudget, budgetSpent(budget)})
				return
			}
			boxRoot, err := boxes[w].open()
			if err != nil {
				r.leave(commitGap{gapRunner, err.Error()})
				return
			}
			runOneCommitMutant(ctx, boxRoot, env, plans[goMutantPackageDir(r.Mutant.File)], r, deadline,
				filepath.Join(work, strconv.Itoa(w), strconv.Itoa(i)), known, confirm)
		}
	}
	// Every mutant gets its cheap run first, so a slow confirmation never
	// keeps a mutant that a selected test kills in seconds waiting for a
	// worker. What the selection did not settle, and what had no selection,
	// is then run against the whole package.
	forEachWorker(workers, all, step(false))
	var pending []int
	for i := range runs {
		if runs[i].pending {
			runs[i].pending = false
			pending = append(pending, i)
		}
	}
	forEachWorker(workers, pending, step(true))
	for _, r := range runs {
		if r.NotMeasured != "" {
			logf(log, "mutants: %s NOT MEASURED — %s", plainName(r.Outcome), r.NotMeasured)
		}
	}
	return runs
}

// killChecks remembers what the run without a mutant said about a set of
// failing tests. The copy the checks run in is the lane as staged, the same
// for every mutant of the run, so the answer for one set of tests is the
// answer for every mutant they fail under: a test that fails under five
// mutants is checked once, not five times. A check that was cut off is not
// remembered, since it said nothing.
type killChecks struct {
	mu   sync.Mutex
	seen map[string]killCheck
}

// killCheck is one remembered answer.
type killCheck struct {
	verdict resolveVerdict
	detail  string
}

// check answers the remembered verdict for the tests named by killers and
// extra, or runs ask and remembers its answer. A nil killChecks remembers
// nothing.
func (k *killChecks) check(killers, extra []string, ask func() (resolveVerdict, string)) (resolveVerdict, string) {
	if k == nil {
		return ask()
	}
	key := strings.Join(killers, "\x00") + "\x01" + strings.Join(extra, "\x00")
	k.mu.Lock()
	got, ok := k.seen[key]
	k.mu.Unlock()
	if ok {
		return got.verdict, got.detail
	}
	verdict, detail := ask()
	if verdict == resolveSurvived || verdict == resolveKilled {
		k.mu.Lock()
		if k.seen == nil {
			k.seen = map[string]killCheck{}
		}
		k.seen[key] = killCheck{verdict, detail}
		k.mu.Unlock()
	}
	return verdict, detail
}

// commitNowFn is the clock a mutant's own time is read from, a seam so a test
// can say how long one took without waiting.
var commitNowFn = time.Now

// budgetSpent says the wall-clock ran out before a mutant could start.
func budgetSpent(budget time.Duration) string {
	return fmt.Sprintf("the run's wall-clock budget of %s was spent before this mutant was run", budget)
}

// commitBox is one worker's disposable copy of the lane, made when its first
// mutant needs it and removed when the worker is done.
type commitBox struct {
	root string
	// worker is which concurrent worker this copy is for, so each keeps its
	// copy at a path of its own that every run reuses (workerSlot).
	worker int
	box    *proveSandbox
	path   string
	err    error
	made   bool
	stop   func()
}

// open answers the root of the worker's copy, making it the first time.
func (b *commitBox) open() (string, error) {
	if b.made {
		return b.path, b.err
	}
	b.made = true
	lane := RepoRoot(b.root)
	if lane == "" {
		b.err = fmt.Errorf("%s is not inside a git repository to copy", b.root)
		return "", b.err
	}
	box, err := newWorkerSandbox(lane, b.root, b.worker)
	if err != nil {
		b.err = fmt.Errorf("a disposable copy of %s to run in could not be made (%v)", lane, err)
		return "", b.err
	}
	b.box = box
	b.stop = watchProveSignals(box.remove, io.Discard)
	if b.path, b.err = box.path(lane, b.root); b.err != nil {
		return "", b.err
	}
	return b.path, nil
}

// close removes the worker's copy.
func (b *commitBox) close() {
	if b.stop != nil {
		b.stop()
	}
	if b.box != nil {
		b.box.remove()
	}
}

// runOneCommitMutant measures one mutant in the copy at root and records the
// answer in run. Without confirm it runs the selected tests and stops there:
// a mutant they kill is caught, and one they miss, or one with no selection,
// is left pending for the run against the whole package, since a map built
// before this commit may not list a test that now reaches the function. With
// confirm it runs the tests the selection did not, which is the whole package
// once the selection has passed.
func runOneCommitMutant(ctx context.Context, root string, env []string, plan *commitPlan, run *commitRun,
	deadline time.Time, work string, known *killChecks, confirm bool) {
	m := run.Mutant
	names, whole := []string(nil), true
	if plan != nil && !plan.Whole {
		names, whole = selectTests(plan.Map, plan.Current, plan.Touched, m.Func)
	}
	run.Selected, run.WholePackage = names, whole
	if whole && !confirm {
		run.pending = true
		return
	}
	path := filepath.Join(root, filepath.FromSlash(m.File))
	src, err := os.ReadFile(path)
	if err != nil {
		run.leave(commitGap{gapRunner, fmt.Sprintf("the source could not be read (%v)", err)})
		return
	}
	mutated, err := mutateAt(src, m.Line, m.Col, m.Mutation)
	if err != nil {
		run.leave(commitGap{gapRunner, err.Error()})
		return
	}
	overlay, err := writeOverlay(work, path, mutated)
	if err != nil {
		run.leave(commitGap{gapRunner, fmt.Sprintf("the overlay could not be written (%v)", err)})
		return
	}
	dir := goMutantPackageDir(m.File)
	var extra []string
	switch {
	case confirm && !whole:
		// The selection passed under this mutant, so the tests left are the
		// rest of the package: together the two runs are the whole package,
		// and no test runs twice.
		run.WholePackage = true
		extra = []string{"-skip", runPattern(names)}
	case !whole:
		extra = []string{"-run", runPattern(names)}
	}
	began := commitNowFn()
	status, gap := settleRun(ctx, root, env, overlay, packageArgs([]string{dir}), extra, deadline, known)
	run.Took += commitNowFn().Sub(began)
	if status == "missed" && !confirm {
		run.pending = true
		return
	}
	if run.leave(gap) {
		return
	}
	run.Outcome.Status = status
	if status == "missed" {
		run.Outcome.Note = "survived the tests of " + dir + ", all of them, run against this one mutant"
	}
}

// settleRun runs the tests once over the overlay and says what came of it:
// "caught", "missed" or "unviable", or, in why, what stopped it from being
// judged. A failure is credited as a kill only when the same run without the
// mutant is green (issue #957).
func settleRun(ctx context.Context, root string, env []string, overlay string, args, extra []string,
	deadline time.Time, known *killChecks) (status string, gap commitGap) {
	// A budget already spent is a run that is cut off at once, which is what
	// runResolveTestsOut answers for a timeout of nothing.
	verdict, detail, output := runResolveTestsOut(ctx, root, env, overlay, args, extra, time.Until(deadline))
	switch verdict {
	case resolveUnviable:
		return "unviable", commitGap{}
	case resolveCutOff:
		return "", cutOffGap(ctx, deadline, detail)
	case resolveSurvived:
		return "missed", commitGap{}
	}
	killers := killerArgs(detail, args)
	// Only the tests that failed are run again: whether they fail without the
	// mutant is the whole question, and the rest of a selection can be most of
	// a package.
	if failing := failingTestNames(output); len(failing) > 0 {
		extra = []string{"-run", runPattern(failing)}
	}
	verdict, detail = known.check(killers, extra, func() (resolveVerdict, string) {
		return runResolveTestsWith(ctx, root, env, "", killers, extra, time.Until(deadline))
	})
	switch verdict {
	case resolveSurvived:
		return "caught", commitGap{}
	case resolveCutOff:
		return "", commitGap{gapUnconfirmed, "the tests of " + packageNames(killers) + " failed under the mutant, and without the mutant " +
			detail + ", so the failure is not shown to be its kill"}
	}
	return "", commitGap{gapUnconfirmed, "the tests of " + packageNames(killers) +
		" fail without the mutant too, so their failure is not its kill"}
}

// failingTestNames is the top-level tests a `go test` output reports failed,
// sorted, once each: a failed subtest is its parent, since -run selects the
// parent. A panic or a timeout prints no such line and names none.
func failingTestNames(output string) []string {
	seen := map[string]bool{}
	for line := range strings.SplitSeq(output, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "--- FAIL: ")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(rest, " ")
		top, _, _ := strings.Cut(name, "/")
		seen[top] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// cutOffGap classifies a run that did not finish: the budget's end, or, with
// time left, a `go test` that could not start.
func cutOffGap(ctx context.Context, deadline time.Time, detail string) commitGap {
	if ctx.Err() != nil || time.Now().After(deadline) {
		return commitGap{gapBudget, detail}
	}
	return commitGap{gapRunner, detail}
}
