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

// Running the mutants of a full run against the tests that execute their
// line (mutants_select_plan.go), where the run builds the index itself, on the
// spot (mutants_select_build.go). A mutant a selection cannot be made for runs
// the package's full suite exactly as a run with no selection does; a mutant
// is never passed on the strength of a selection that might have missed a test.

// gapFlaky is a mutant a test killed once and did not kill on a second run:
// the kill is not trusted, and neither is a survivor claimed.
const gapFlaky = "flaky"

// The ways a mutant was judged.
const (
	selModeSelected   = "selected"
	selModeFull       = "full"
	selModeNotCovered = "not-covered"
)

// selMutant is one mutant to judge.
type selMutant struct {
	File     string
	Line     int
	Col      int
	Mutation string
}

// selResult is what became of one mutant.
type selResult struct {
	// Status is caught, missed or unviable when the mutant was judged; Gap says
	// why it was not otherwise.
	Status string
	Note   string
	Gap    commitGap
	// Killer names the run whose failure caught it, as "pkg: TestA, TestB".
	Killer string
	// Mode is how it was run, Why the reason it ran the full suite.
	Mode string
	Why  string
	// Tests is how many tests were started for it, the whole package counted by
	// its size where the index knows it.
	Tests int
	Took  time.Duration
}

// selStats is what a run did, for its summary.
type selStats struct {
	Selected, Tests, NotCovered, Full, Flaky int
	FullWhy                                  map[string]int
	// Build is the time spent building coverage, Packages how many packages'
	// tests it covers, Extra how many of those are not under mutation, Kept how
	// many tag sets were read from the kept coverage.
	Build           time.Duration
	Packages, Extra int
	Kept            int
}

// selRunner judges mutants with a selection. Its methods are for one goroutine
// at a time: the box the mutants run in is one copy.
type selRunner struct {
	root  string
	cfg   MutantsConfig
	sets  []selSet
	known *killChecks
	log   io.Writer
	env   []string
	// open answers the root of the copy the mutants run in.
	open  func() (string, error)
	close func()

	mu    sync.Mutex
	stats selStats
}

// selTagSets are the sets a run measures, the unit tests first.
func selTagSets(cfg MutantsConfig) []selTagSet {
	sets := []selTagSet{{Label: "unit"}}
	if len(cfg.TestTags) > 0 {
		sets = append(sets, selTagSet{Label: "tags", Tags: slices.Clone(cfg.TestTags)})
	}
	return sets
}

// newSelRunner builds the index of every tag set for the packages under
// mutation, now, in the foreground, and answers a runner over them. A set that
// cannot be had leaves its reason, and every mutant then runs the full suite.
// The caller closes the runner.
func newSelRunner(ctx context.Context, root string, cfg MutantsConfig, targets []string, workers int, log io.Writer) *selRunner {
	box := &commitBox{root: root}
	r := &selRunner{root: root, cfg: cfg, known: &killChecks{}, log: log, env: measureEnv(root, cfg), open: box.open, close: box.close}
	for _, set := range selTagSets(cfg) {
		b := buildSelIndex(ctx, root, cfg, set, targets, workers, box, log)
		r.sets = append(r.sets, selSet{Label: set.Label, Tags: set.Tags, Idx: b.Idx, Why: b.Why, Cheap: b.Cheap})
		r.stats.Build += b.Took
		r.stats.Packages = max(r.stats.Packages, b.Packages)
		r.stats.Extra = max(r.stats.Extra, b.Extra)
		if b.Hit {
			r.stats.Kept++
		}
		if b.Idx == nil && len(b.Cheap) == 0 {
			logf(log, "mutants: no per-test coverage for the %s tests (%s): their mutants run the package's full suite", set.Label, b.Why)
		}
	}
	return r
}

// judge decides and runs one mutant on the selection. ok is false when a
// selection cannot be made: res then says why (Mode full) and the caller runs
// the full suite, as it would with no selection.
func (r *selRunner) judge(ctx context.Context, m selMutant, budget time.Duration, work string) (res selResult, ok bool) {
	plan := planSelection(r.sets, r.root, m.File, m.Line, m.Col)
	switch {
	case plan.Full != "":
		res = selResult{Mode: selModeFull, Why: plan.Full}
		r.record(res)
		return res, false
	case plan.NotCovered:
		res = selResult{Mode: selModeNotCovered, Gap: commitGap{gapNotCovered, "no test of the module executes this line, so none could kill it"}}
		r.record(res)
		return res, true
	}
	res = r.execPlan(ctx, m, plan, budget, work)
	res.Mode = selModeSelected
	r.record(res)
	return res, true
}

// settle judges a mutant on the selection, or, where none can be made, on the
// mutated package's full suite, unit tests then each tagged set.
func (r *selRunner) settle(ctx context.Context, m selMutant, budget time.Duration, work string) selResult {
	res, ok := r.judge(ctx, m, budget, work)
	if ok {
		return res
	}
	full := selPlan{}
	for _, s := range r.sets {
		full.Stages = append(full.Stages, selStage{Label: s.Label, Tags: s.Tags, Runs: []selRun{{Pkg: goMutantPackageDir(m.File), Whole: true}}})
	}
	out := r.execPlan(ctx, m, full, budget, work)
	out.Mode, out.Why = selModeFull, res.Why
	return out
}

// execPlan applies the mutant in an overlay and runs the plan's stages in
// order, stopping at the first that settles it. A kill is run once more: if
// the second run passes, the mutant is flaky and neither caught nor missed.
func (r *selRunner) execPlan(ctx context.Context, m selMutant, plan selPlan, budget time.Duration, work string) (res selResult) {
	began := commitNowFn()
	defer func() { res.Took = commitNowFn().Sub(began) }()
	root, err := r.open()
	if err != nil {
		res.Gap = commitGap{gapRunner, err.Error()}
		return res
	}
	srcPath := filepath.Join(root, filepath.FromSlash(m.File))
	src, err := os.ReadFile(srcPath)
	if err != nil {
		res.Gap = commitGap{gapRunner, fmt.Sprintf("the source could not be read (%v)", err)}
		return res
	}
	mutated, err := mutateAt(src, m.Line, m.Col, m.Mutation)
	if err != nil {
		res.Gap = commitGap{gapRunner, err.Error()}
		return res
	}
	overlay, err := writeOverlay(work, srcPath, mutated)
	if err != nil {
		res.Gap = commitGap{gapRunner, fmt.Sprintf("the overlay could not be written (%v)", err)}
		return res
	}
	deadline := time.Now().Add(budget)
	var ran []string
	for _, stage := range plan.Stages {
		for _, run := range stage.Runs {
			args := packageArgs([]string{run.Pkg})
			extra := selRunExtra(stage.Tags, run)
			res.Tests += r.testsOf(stage.Label, run)
			ran = append(ran, run.Pkg)
			status, gap := settleRun(ctx, root, r.env, overlay, args, extra, tagsFlag(stage.Tags), deadline, r.known)
			if gap.Why != "" {
				res.Gap = gap
				return res
			}
			switch status {
			case "unviable":
				res.Status = "unviable"
				return res
			case "caught":
				again, gap := settleRun(ctx, root, r.env, overlay, args, extra, tagsFlag(stage.Tags), deadline, r.known)
				switch {
				case gap.Why != "":
					res.Gap = gap
				case again == "missed":
					res.Gap = commitGap{gapFlaky, "the tests of " + run.Pkg + " failed under the mutant once and passed on a second run, so the kill is not trusted"}
				default:
					res.Status = "caught"
					res.Killer = selRunName(run)
				}
				return res
			}
		}
	}
	res.Status = "missed"
	res.Note = fmt.Sprintf("survived the %d test(s) run for its line in %s", res.Tests, strings.Join(slices.Compact(ran), ", "))
	return res
}

// testsOf is how many tests a run starts: the named ones, or the package's
// whole suite where the set's index knows its size.
func (r *selRunner) testsOf(label string, run selRun) int {
	if !run.Whole {
		return len(run.Names)
	}
	for _, s := range r.sets {
		if s.Label == label && s.Idx != nil {
			return s.Idx.PkgTests[run.Pkg]
		}
	}
	return 0
}

// record counts one mutant's outcome.
func (r *selRunner) record(res selResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch res.Mode {
	case selModeSelected:
		r.stats.Selected++
		r.stats.Tests += res.Tests
	case selModeNotCovered:
		r.stats.NotCovered++
	case selModeFull:
		r.stats.Full++
		if r.stats.FullWhy == nil {
			r.stats.FullWhy = map[string]int{}
		}
		r.stats.FullWhy[res.Why]++
	}
	if res.Gap.Kind == gapFlaky {
		r.stats.Flaky++
	}
}

// summary is the run's line: how many mutants ran selected tests, how many the
// full suite and why, how many were not covered, and what the coverage cost.
func (r *selRunner) summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stats
	var b strings.Builder
	fmt.Fprintf(&b, "mutants: selection — %d ran selected tests", s.Selected)
	if s.Selected > 0 {
		mean := float64(s.Tests) / float64(s.Selected)
		fmt.Fprintf(&b, " (%s test(s) per mutant on average)", strconv.FormatFloat(float64(int(mean*10+0.5))/10, 'f', -1, 64))
	}
	fmt.Fprintf(&b, ", %d ran the full suite", s.Full)
	if s.Full > 0 {
		reasons := make([]string, 0, len(s.FullWhy))
		for why := range s.FullWhy {
			reasons = append(reasons, why)
		}
		slices.Sort(reasons)
		parts := make([]string, len(reasons))
		for i, why := range reasons {
			parts[i] = fmt.Sprintf("%d %s", s.FullWhy[why], why)
		}
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, ", %d not-covered", s.NotCovered)
	if s.Flaky > 0 {
		fmt.Fprintf(&b, ", %d flaky", s.Flaky)
	}
	fmt.Fprintf(&b, "; coverage of %d package(s), %d beyond the mutated one, built in %s", s.Packages, s.Extra, s.Build.Round(time.Second))
	if s.Kept > 0 {
		fmt.Fprintf(&b, " (%d tag set(s) read from the kept coverage)", s.Kept)
	}
	return b.String()
}

// selRunName names a run for a note: the package and the tests it ran.
func selRunName(run selRun) string {
	if run.Whole {
		return run.Pkg + ": every test"
	}
	return run.Pkg + ": " + strings.Join(run.Names, ", ")
}

// selSettleWorkers is how many tests of the unit set run at once while the
// coverage of a settle run is built.
func selSettleWorkers() int {
	jobs, _ := mutantsJobsForThisBoxFn(mutantsGoJobGB)
	return max(1, jobs)
}

// newSettleSelection builds the per-test coverage the settle of the mutants at
// idx is judged on, for the packages they sit in, within the run's settle cap.
// It answers nil when there is no selection to make: the module's package
// graph could not be read, which the settle refuses on its own account.
func newSettleSelection(ctx context.Context, root string, cfg MutantsConfig, outcomes []MutantOutcome, idx []int, graphErr error, log io.Writer) *selRunner {
	if graphErr != nil {
		return nil
	}
	var targets []string
	for _, i := range idx {
		if dir := goMutantPackageDir(outcomes[i].File); !slices.Contains(targets, dir) {
			targets = append(targets, dir)
		}
	}
	slices.Sort(targets)
	ctx, cancel := context.WithTimeout(ctx, resolveTotalCap)
	defer cancel()
	return newSelRunner(ctx, root, cfg, targets, selSettleWorkers(), log)
}

// settledBySelection is the outcome m becomes when a selection judged it. A
// mutant left without a verdict keeps its status and says why.
func settledBySelection(m MutantOutcome, res selResult) MutantOutcome {
	switch {
	case res.Gap.Kind == gapNotCovered:
		m.Note = res.Gap.Why
	case res.Gap.Why != "":
		return unresolved(m, res.Gap.Why)
	case res.Status == "caught":
		m.Status, m.Note = "caught", "killed by "+res.Killer+" (settled by running the tests that execute its line)"
	case res.Status == "unviable":
		m.Status, m.Note = "unviable", "does not compile, so no test can run it (settled by running this one mutant)"
	default:
		m.Status, m.Note = "missed", res.Note+", every test that executes its line"
	}
	return m
}
