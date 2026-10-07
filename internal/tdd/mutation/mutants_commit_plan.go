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
		// A test that starts its own binary again (the helper-process pattern) runs
		// the code under test in a child whose coverage the parent's profile does
		// not hold, so no map can say which tests execute a line: run it whole.
		whole = whole || reexecsTestBinary(filepath.Join(root, filepath.FromSlash(dir)))
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

// measureTestMaps gives each plan the map of its package, each package within
// an equal share of the time left: the kept one when the
// package's content and toolchain are those it was measured at, else one
// coverage run of the package's tests, in the foreground, within ctx's
// deadline, which is what is left of the commit's budget, kept for the next
// commit to the same content. A package whose tests are run whole anyway
// has no use for one. A package whose coverage cannot be measured, or not in
// the time left, is named on log and keeps no map: its mutants run the commit's
// touched tests and then the whole package, and what the budget does not reach
// is NOT MEASURED.
func measureTestMaps(ctx context.Context, root string, cfg MutantsConfig, plans map[string]*commitPlan, workers int, log io.Writer) {
	dirs := make([]string, 0, len(plans))
	for dir, plan := range plans {
		if !plan.Whole {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	for i, dir := range dirs {
		// Each package gets an equal share of what is left, so the first one
		// cannot spend the whole budget and leave the rest no time at all.
		pctx, cancel := ctx, context.CancelFunc(func() {})
		if deadline, ok := ctx.Deadline(); ok {
			pctx, cancel = context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(dirs)-i))
		}
		m, ok, _, err := ensureTestMap(pctx, root, cfg, dir, workers, log)
		cancel()
		if err != nil {
			logf(log, "mutants: coverage of %s NOT MEASURED — %v", dir, err)
			continue
		}
		if ok {
			plans[dir].Map = &m
		}
	}
}

// reexecsTestBinary reports whether a test file of the package in dir starts
// the test binary again, by naming os.Args[0] or os.Executable, which is how a
// helper-process test runs the code under test in a child. The profile of the
// parent holds nothing of what the child executed.
func reexecsTestBinary(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		src := string(data)
		if strings.Contains(src, "os.Args[0]") || strings.Contains(src, "os.Executable(") {
			return true
		}
	}
	return false
}
