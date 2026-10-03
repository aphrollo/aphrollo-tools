package suite

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// #1172: a merge's `go test -race` ran over every package the lane touched AND
// every package that imports one of them. A lane that touches a hub package
// (the one openapi and sqlc regeneration land in) has thirty importers, each
// compiled and run at -race's several-fold price, for tests of code the lane
// did not change.
//
// -race now covers the packages the lane changed. Their importers run in a
// second command with the same -count=1 -shuffle=on and no -race: a change in
// a package can break an importer's tests, which the plain run still proves,
// but a data race in an importer's own code is that importer's own change to
// answer for. A merge is green only when both runs are.
//
// A repo that wants -race over everything says so with `race-scope = "all"`
// in [aphrollo], and its merge keeps the single run it had.

// raceScopeKey is the [aphrollo] key choosing what -race covers at the merge:
// the changed packages (the default) or "all" of the run.
const raceScopeKey = "race-scope"

// raceEverywhere reports whether repoRoot's aphrollo.toml asks for -race over
// the whole merge run.
func raceEverywhere(repoRoot string) bool {
	v, set := tomlStringIn(filepath.Join(repoRoot, "aphrollo.toml"), "[aphrollo]", raceScopeKey)
	return set && strings.EqualFold(strings.TrimSpace(v), "all")
}

// splitRaceRuns is full, a `go test -race` run, as the runs that cover the
// same packages with -race only where files changed them: the race run over
// the changed packages first, then the importers without it. files are the
// changed files relative to root, the list full was narrowed from.
//
// It answers full alone, as the one run it was, whenever it cannot split
// without guessing: not a `go test -race` run, a repo that asked for -race
// everywhere, a package list it cannot read back (the whole-module fallback),
// or nothing on one side of the cut.
func splitRaceRuns(full Runner, repoRoot, root string, files []string) []Runner {
	if full.Cmd != "go" || !hasRaceFlag(full) || raceEverywhere(repoRoot) {
		return []Runner{full}
	}
	pkgs, with := argvbatch.GoTestPackages(full.Cmd, full.Args)
	if pkgs == nil {
		return []Runner{full}
	}
	changed := map[string]bool{}
	for _, dir := range goStagedDirs(root, files) {
		changed[goPackagePattern(dir)] = true
	}
	var racePkgs, plainPkgs []string
	for _, p := range pkgs {
		if changed[p] {
			racePkgs = append(racePkgs, p)
		} else {
			plainPkgs = append(plainPkgs, p)
		}
	}
	if len(racePkgs) == 0 || len(plainPkgs) == 0 {
		return []Runner{full}
	}
	race, plain := full, full
	race.Args = with(racePkgs)
	plain.Args = slices.DeleteFunc(with(plainPkgs), func(a string) bool { return a == "-race" })
	return []Runner{race, plain}
}

// goPackagePattern is the `go test` argument naming the package in dir, as
// narrowToStaged spells it.
func goPackagePattern(dir string) string {
	if dir == goRootPackageKey {
		return goRootPackageKey
	}
	return "./" + dir
}
