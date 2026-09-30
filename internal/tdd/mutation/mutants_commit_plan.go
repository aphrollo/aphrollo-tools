package mutation

import (
	"context"
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
// now, the ones this commit touched, and the kept map. A package with no map
// gets a plan with a nil map, which runs the whole package.
func commitPlans(root string, mutants []commitMutant, added map[string]map[int]bool) map[string]*commitPlan {
	plans := map[string]*commitPlan{}
	for _, m := range mutants {
		dir := goMutantPackageDir(m.File)
		if plans[dir] != nil {
			continue
		}
		decls := scanTestDecls(filepath.Join(root, filepath.FromSlash(dir)), dir)
		touched, whole := testChanges(decls, added)
		kept, _ := loadTestMap(root, dir)
		plans[dir] = &commitPlan{Dir: dir, Map: kept, Current: testNames(decls), Touched: touched, Whole: whole}
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
// gate's tests should start a real mutant.
func SetCommitExecForTest(fn func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error)) (restore func()) {
	prev := resolveExecFn
	resolveExecFn = fn
	return func() { resolveExecFn = prev }
}
