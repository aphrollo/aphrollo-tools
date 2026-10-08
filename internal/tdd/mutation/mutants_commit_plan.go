package mutation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// What the commit stage reads before it runs anything: the lines the staged
// change adds, the sources those lines are in, and the tests the runner may
// select from for each package.

// stagedAddedLines answers, per repo-relative file, the lines the staged
// change adds or changes against HEAD.
func stagedAddedLines(root string) (map[string]map[int]bool, error) {
	out, errText, err := gitDiffOutFn(root, "diff", "--cached", "--no-color", "--no-ext-diff", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", "-U0", "--")
	if err != nil {
		return nil, fmt.Errorf("git diff --cached -U0: %s", gitFailureText(errText, err))
	}
	return parseAddedLines(out), nil
}

// unstagedFiles is the files whose working copy differs from the index. The
// runner copies the working tree, so a mutant of such a file would sit in
// code the commit does not hold.
func unstagedFiles(root string) (map[string]bool, error) {
	out, errText, err := gitDiffOutFn(root, "diff", "--name-only", "--no-color", "--no-ext-diff", "--")
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %s", gitFailureText(errText, err))
	}
	files := map[string]bool{}
	for name := range strings.SplitSeq(out, "\n") {
		if name = strings.TrimSpace(name); name != "" {
			files[name] = true
		}
	}
	return files, nil
}

// isCommitSource reports whether a staged path is Go production code the
// commit stage mutates: a Go file that is not a test and not test data or
// vendored.
func isCommitSource(file string) bool {
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return false
	}
	return !isMutationData(file) && !strings.Contains("/"+file, "/vendor/")
}

// isMutationData reports whether a repo-relative path lies in a tree that is
// data for a test and never code of the repo: a testdata directory, or the
// fixtures of a ratchet law. Every mutation path leaves these out, whatever
// their extension says.
func isMutationData(file string) bool {
	slashed := "/" + filepath.ToSlash(file)
	return strings.Contains(slashed, "/testdata/") || strings.Contains(slashed, "/.ratchet/fixtures/")
}

// commitMutantsOf lists the mutants of every staged source on the lines the
// commit adds, and the notes for the sources it left out.
func commitMutantsOf(root string, added map[string]map[int]bool, unstaged map[string]bool) (mutants []commitMutant, notes []string) {
	files := make([]string, 0, len(added))
	for file := range added {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		if !isCommitSource(file) || !inThisBuild(path) {
			continue
		}
		if unstaged[file] {
			notes = append(notes, file+" has unstaged edits, so its mutants are not measured: the run would judge code this commit does not hold")
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			// absence-ok: a file the diff names but the tree lacks (deleted or renamed away) has no mutants to measure
			continue
		}
		mutants = append(mutants, enumerateCommitMutants(file, src, added[file])...)
	}
	return mutants, notes
}

// commitPlans reads, for each package the mutants sit in, the tests it has
// now and the ones this commit touched. The map is filled in by
// measureTestMaps; a package whose map is not (yet) there has a nil one, which
// runs the commit's own touched tests first and then the whole package.
func commitPlans(root string, mutants []commitMutant, added map[string]map[int]bool) map[string]*commitPlan {
	plans := map[string]*commitPlan{}
	for _, m := range mutants {
		dir := goMutantPackageDir(m.File)
		if plans[dir] != nil {
			continue
		}
		decls := scanTestDecls(filepath.Join(root, filepath.FromSlash(dir)), dir)
		touched, whole := testChanges(decls, added)
		plans[dir] = &commitPlan{Dir: dir, Current: testNames(decls), Touched: touched, Whole: whole}
	}
	return plans
}

// commitReport renders what a run found: the verdict over the mutants that
// were measured, and one line counting the ones that were not, by kind.
func commitReport(cfg MutantsConfig, runs []commitRun) (Verdict, int, string) {
	var outcomes []MutantOutcome
	gaps := map[string]int{}
	for _, r := range runs {
		if r.NotMeasured != "" {
			gaps[r.GapKind]++
			continue
		}
		outcomes = append(outcomes, r.Outcome)
	}
	return judgeMutants(cfg, outcomes), len(outcomes), gapSummary(gaps)
}

// gapSummary is "3 budget, 1 runner" for the mutants left unmeasured, "" for
// none.
func gapSummary(gaps map[string]int) string {
	kinds := make([]string, 0, len(gaps))
	for kind := range gaps {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	parts := make([]string, len(kinds))
	for i, kind := range kinds {
		parts[i] = fmt.Sprintf("%d %s", gaps[kind], kind)
	}
	return strings.Join(parts, ", ")
}

// commitHeadroomFn asks whether the box has the memory to start a measurement
// without waiting for it. A seam so a test can say it has not.
var commitHeadroomFn = WaitForHeadroom

// SetCommitHeadroomForTest replaces the headroom probe and answers the
// restore. Exported because the commit gate's own tests, in the package above,
// prove what the stage does on a box with no memory to spare.
func SetCommitHeadroomForTest(fn func(dir string, wait time.Duration) string) (restore func()) {
	prev := commitHeadroomFn
	commitHeadroomFn = fn
	return func() { commitHeadroomFn = prev }
}

// SetCommitExecForTest replaces the `go test` spawn of the commit-time run
// and answers the restore, for the same reason: no box running the commit
// gate's tests should start a real mutant. The coverage build the stage makes
// first is refused the same way: its commands fail to start, and the mutants
// run as they do with no map.
func SetCommitExecForTest(fn func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error)) (restore func()) {
	prev, prevMap := resolveExecFn, testMapExecFn
	resolveExecFn = fn
	testMapExecFn = func(context.Context, string, []string, []string, io.Writer) (int, error) {
		return 1, errors.New("no toolchain in a unit test")
	}
	return func() { resolveExecFn, testMapExecFn = prev, prevMap }
}

// coverageShare is the part of what is left of the commit's budget the
// coverage phase may spend; the mutant runs keep the rest, so a package whose
// coverage is slow cannot leave its mutants no time.
const coverageShare = 2

// fillShare is the part of a package's coverage share the tests beyond the
// ones its mutants need may start in.
const fillShare = 2

// coverTimeoutFn and coverUntilFn are how the coverage phase cuts its budget
// into shares: a deadline and the time left to it. Seams, so a test that says
// how the shares divide does so on a clock of its own, not on how long the box
// takes to get to the second package.
var (
	coverTimeoutFn = context.WithTimeout
	coverUntilFn   = time.Until
)

// measureTestMaps gives each plan the map of its package, each package within
// an equal share of the coverage phase: what the store holds and still holds,
// plus the tests measured now for the functions the mutants sit in, in the
// foreground, within ctx's deadline, and kept for the next commit. A package
// whose tests are run whole anyway has no use for one. A package whose
// coverage cannot be measured is named on log and keeps no map: its mutants
// run the commit's touched tests and then the whole package, and what the
// budget does not reach is NOT MEASURED. The build runs in boxes[0], the copy
// the mutant runs use, when there is one.
func measureTestMaps(ctx context.Context, root string, cfg MutantsConfig, plans map[string]*commitPlan, mutants []commitMutant, workers int, boxes []*commitBox, log io.Writer) {
	dirs := make([]string, 0, len(plans))
	for dir, plan := range plans {
		if !plan.Whole {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	phase, endPhase := ctx, context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok {
		phase, endPhase = coverTimeoutFn(ctx, coverUntilFn(deadline)/coverageShare)
	}
	defer endPhase()
	for i, dir := range dirs {
		// Each package gets an equal share of what is left of the phase, so the
		// first one cannot spend it all and leave the rest no time.
		pctx, cancel := phase, context.CancelFunc(func() {})
		if deadline, ok := phase.Deadline(); ok {
			pctx, cancel = coverTimeoutFn(phase, coverUntilFn(deadline)/time.Duration(len(dirs)-i))
		}
		req := covRequest{Dir: dir, Workers: workers}
		if deadline, ok := pctx.Deadline(); ok {
			// The tests the mutants need come first and may use the package's whole
			// share; the rest of the package is measured only in the first half of
			// it, so a commit that has to compile anyway also builds toward a
			// complete map without spending its whole budget on one.
			req.FillBy = commitNowFn().Add(coverUntilFn(deadline) / fillShare)
		}
		if len(boxes) > 0 {
			req.Box = boxes[0]
		}
		for _, m := range mutants {
			if goMutantPackageDir(m.File) == dir {
				req.Mutants = append(req.Mutants, m)
			}
		}
		res, err := ensureCoverage(pctx, root, cfg, req, log)
		cancel()
		if err != nil {
			logf(log, "mutants: coverage of %s NOT MEASURED — %v", dir, err)
			continue
		}
		if res.Built {
			plans[dir].Map = &res.Map
		}
	}
}
