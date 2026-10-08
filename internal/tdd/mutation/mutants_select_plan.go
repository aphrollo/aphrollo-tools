package mutation

import (
	"slices"
	"strings"
)

// What a full run runs for one mutant: the tests that execute its line, the
// unit tests first and then, only if it survived those, the tests of each
// tagged set that were not already run. A line no test executes is
// not-covered and runs nothing. Any doubt runs the package's full suite, as a
// run with no selection does, and never passes the mutant.

// The reasons a mutant runs the full suite instead of a selection.
const (
	selWhyNoEntry   = "no-entry"
	selWhyBuild     = "build-failed"
	selWhyList      = "list-failed"
	selWhyFailed    = "test-failed-alone"
	selWhyNoProfile = "no-profile"
	selWhyStale     = "stale-shape"
	selWhyUnlisted  = "unlisted-line"
)

// selSet is one tag set as the plan reads it: its index, or why it has none.
type selSet struct {
	Label string
	Tags  []string
	Idx   *selIndex
	// Why is the reason Idx is nil.
	Why string
}

// selRun is the tests of one package a stage runs: the named ones, or the
// whole package.
type selRun struct {
	Pkg   string
	Names []string
	Whole bool
}

// selStage is one tag set's runs.
type selStage struct {
	Label string
	Tags  []string
	Runs  []selRun
}

// selPlan is what one mutant runs.
type selPlan struct {
	Stages []selStage
	// NotCovered says no test executes the line: nothing is run.
	NotCovered bool
	// Full is the reason the mutant runs the whole package as a run with no
	// selection does, "" when a selection was made.
	Full string
}

// planSelection decides what a mutant on the line of file (module-relative)
// runs. root is the tree the file is read from, to check the index's lines
// still hold for it.
func planSelection(sets []selSet, root, file string, line int) selPlan {
	pkg := goMutantPackageDir(file)
	type found struct {
		set   selSet
		tests []selTest
		whole []string
	}
	var all []found
	anyListed := false
	for _, s := range sets {
		if s.Idx == nil {
			return selPlan{Full: s.Why}
		}
		if why := s.Idx.Doubt[pkg]; why != "" {
			return selPlan{Full: why}
		}
		tests, listed := s.Idx.testsAt(file, line)
		if listed {
			anyListed = true
			if s.Idx.stale(root, file) != "" {
				return selPlan{Full: selWhyStale}
			}
		}
		f := found{set: s, tests: tests}
		for dir := range s.Idx.Doubt {
			f.whole = append(f.whole, dir)
		}
		slices.Sort(f.whole)
		all = append(all, f)
	}
	if !anyListed {
		return selPlan{Full: selWhyUnlisted}
	}
	var plan selPlan
	ran := map[selTest]bool{}
	for _, f := range all {
		stage := selStage{Label: f.set.Label, Tags: f.set.Tags}
		byPkg := map[string][]string{}
		for _, t := range f.tests {
			if !ran[t] {
				byPkg[t.Pkg] = append(byPkg[t.Pkg], t.Name)
			}
			ran[t] = true
		}
		for _, dir := range f.whole {
			delete(byPkg, dir)
		}
		pkgs := make([]string, 0, len(byPkg)+len(f.whole))
		for dir := range byPkg {
			pkgs = append(pkgs, dir)
		}
		pkgs = append(pkgs, f.whole...)
		slices.SortFunc(pkgs, func(a, b string) int {
			switch {
			case a == b:
				return 0
			case a == pkg:
				return -1
			case b == pkg:
				return 1
			}
			return strings.Compare(a, b)
		})
		for _, dir := range pkgs {
			if names, ok := byPkg[dir]; ok {
				slices.Sort(names)
				stage.Runs = append(stage.Runs, selRun{Pkg: dir, Names: names})
			} else {
				stage.Runs = append(stage.Runs, selRun{Pkg: dir, Whole: true})
			}
		}
		if len(stage.Runs) > 0 {
			plan.Stages = append(plan.Stages, stage)
		}
	}
	plan.NotCovered = len(plan.Stages) == 0
	return plan
}

// selRunExtra is the `go test` flags that select a run's tests: the tag set's
// tags and an anchored `-run` pattern over the sorted, unique names, which
// matches no test of which a name is only a prefix. A run that is whole, or
// whose pattern is too long for a command line, adds no `-run`.
func selRunExtra(tags []string, r selRun) []string {
	extra := tagsFlag(tags)
	if r.Whole {
		return extra
	}
	names := slices.Clone(r.Names)
	slices.Sort(names)
	names = slices.Compact(names)
	pattern := runPattern(names)
	if len(names) == 0 || len(pattern) > maxRunPatternLen {
		return extra
	}
	return append(extra, "-run", pattern)
}
