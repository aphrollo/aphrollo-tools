package tdd

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Issue #695. gremlins judges a mutant with the MUTATED PACKAGE's own tests,
// and there is no argv of ours to widen: the selection is inside the tool.
// Measured on a three-package module, the killing test's PRESENCE made no
// difference to the verdict and only its LOCATION did — in the importing
// package, Killed 0 Lived 1; deleted outright, Killed 0 Lived 1; moved into
// the mutated package, Killed 1 Lived 0.
//
// So what is fixed here is the CLAIM rather than the run. A Lived verdict
// means "the mutated package's tests did not kill it", and that is the whole
// story only when the mutated package's tests were the whole opportunity.
// Where a package OUTSIDE it has tests that reach the mutated code, gremlins
// could not have observed the test that kills the mutant, and the rule every
// other untested verdict in this gate already obeys applies: a verdict may
// only claim what the run it was read from could have observed.
//
// That mutant is INCONCLUSIVE, in the same words the Go proof arm uses for
// the reach it cannot establish (scopeUnknownAdvisory, exit
// ExitMutantsProveScopeUnknown) — someone reading a mutation result should
// not have to know which stage or which language produced it to know what it
// means. It is reported by name with its reason and it does not refuse the
// merge.
//
// The other half must not move: where nothing outside the mutated package
// has tests reaching it, its own tests WERE the whole opportunity, the Lived
// verdict is a real survivor, and it still blocks.

// gremlinsScopeUnknown is the status of a survivor whose selection could not
// have observed a killing test. It is the one status this package invents
// rather than reads off a producer, because it is a fact about the RUN — the
// selection gremlins chose — that the producer never reports.
const gremlinsScopeUnknown = "scopeunknown"

// classifyGoSurvivorReach re-reads every survivor in a gremlins run against
// the module's own reach graph, downgrading to gremlinsScopeUnknown each one
// whose killing test could only have lived outside the selection gremlins
// ran. Outcomes that are not survivors are returned untouched, and the input
// slice is never written to.
//
// The graph is read ONCE for the whole run, and only when there is a survivor
// to classify: a run that caught everything pays nothing, and a run with
// forty survivors pays the same single `go list` as a run with one.
func classifyGoSurvivorReach(root string, mutants []MutantOutcome) []MutantOutcome {
	if !anySurvivor(mutants) {
		return mutants
	}
	g, err := goReachGraphFn(root)
	out := make([]MutantOutcome, len(mutants))
	copy(out, mutants)
	for i, m := range out {
		if m.Status != "missed" {
			continue
		}
		if err != nil {
			// NOT an empty answer: "nothing else reaches this package" and "I
			// could not find out" are opposite claims, and only the first one
			// could ever ground a survivor.
			out[i] = scopeUnknownOutcome(m, fmt.Sprintf("the module's own package graph could not be read (%v)", err))
			continue
		}
		dir := goMutantPackageDir(m.File)
		if !g.Pkgs[dir] {
			out[i] = scopeUnknownOutcome(m, fmt.Sprintf(
				"`go list` named no package at %s, so which tests reach this code could not be established", dir))
			continue
		}
		if outside := testedPackagesReaching(g, dir); len(outside) > 0 {
			out[i] = scopeUnknownOutcome(m, fmt.Sprintf(
				"gremlins judged it with %s's own tests, and %s outside it %s tests that reach this code",
				dir, strings.Join(outside, ", "), reachVerb(len(outside))))
		}
	}
	return out
}

// anySurvivor reports whether the run has anything for the graph to be read
// for.
func anySurvivor(mutants []MutantOutcome) bool {
	for _, m := range mutants {
		if m.Status == "missed" {
			return true
		}
	}
	return false
}

// testedPackagesReaching names the packages OTHER than dir whose test binary
// can reach dir and which have a test file to run, sorted.
//
// The test-file filter is what keeps a genuine survivor blocking: an importer
// with no _test.go of its own reaches the mutated code and can never kill
// anything in it, so counting it would turn every survivor in an imported
// package into an inconclusive one.
func testedPackagesReaching(g goReachGraph, dir string) []string {
	var out []string
	for _, p := range g.Reaching(dir) {
		if p != dir && g.Tested[p] {
			out = append(out, p)
		}
	}
	return out
}

// reachVerb agrees the reason's verb with however many packages it names.
func reachVerb(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// scopeUnknownOutcome is one survivor turned into the inconclusive verdict,
// carrying the reason it could not be judged.
func scopeUnknownOutcome(m MutantOutcome, why string) MutantOutcome {
	m.Status = gremlinsScopeUnknown
	m.Note = scopeUnknownMutantNote(why)
	return m
}

// scopeUnknownMutantNote is what the report says about such a mutant. It
// reads like scopeUnknownAdvisory, the proof arm's verdict for a reach it
// cannot establish, because it means the same thing.
func scopeUnknownMutantNote(why string) string {
	return "mutant SCOPE UNKNOWN: " + why + ". INCONCLUSIVE — nothing is proved either way, because a " +
		"survivor claim asserts that no test kills the line and this run could not have observed one outside " +
		"the mutated package. Not counted as a survivor and not refused — run the tests that reach this code " +
		"by hand, or move one that kills it into the mutated package"
}

// goMutantPackageDir is the package directory a gremlins mutant sits in, in
// goPackageDir's dialect: repo-relative, forward-slashed, "." for a file in
// the module root.
func goMutantPackageDir(file string) string {
	dir := path.Dir(filepath.ToSlash(file))
	if dir == "" {
		return "."
	}
	return dir
}
