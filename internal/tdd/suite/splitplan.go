package suite

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
	"github.com/aphrollo/aphrollo-tools/internal/config"
)

// A `go test -race` run over twenty packages is one command with one budget.
// On a box where -race is slow it times out whole: three packages took 301s,
// 522s and then 600s on one Windows box, and twenty never finished, so a merge
// that touched them could not be proven and could not land. The work was not
// untestable; one budget was asked to cover all of it.
//
// A plan cuts the package list into runs that each fit the per-run budget,
// judged from what each package was recorded to cost (pkgsecs.go) and a
// default for the ones never recorded, and runs them within one overall cap.
// Every package is still in exactly one run, with the flags it always had.
//
// Three rules keep the change to the safe direction:
//
//	a run that fits is not touched. A list the records say fits one budget is
//	   the one command it always was; only a list that would not fit is cut.
//	a timeout is never a pass. Any run that ends unfinished makes the whole
//	   result unfinished, and the result names the packages that did not.
//	the width is the box's, not the plan's. Runs go side by side only up to
//	   the build-slot count, and each still starts through the memory-headroom
//	   wait and under the memory cap every suite spawn goes through.

const (
	// unknownRacePkgSecs is what a package with no record is planned at under
	// -race, and unknownPlainPkgSecs without it (about a third as much): figures
	// from the runs this box produced, high enough that twenty strangers are
	// cut into runs that finish, low enough that a handful of them stay one run.
	unknownRacePkgSecs  = 90.0
	unknownPlainPkgSecs = 30.0
)

// GoTestPlan is how one `go test` run of a package list is carried out: as
// the one command it was, or as several runs each within its budget. The zero
// value is not a plan; PlanGoTestRun makes one.
type GoTestPlan struct {
	// recordable is whether the line is one whose per-package seconds mean
	// something and that can be rebuilt over a subset: a `go test` of a package
	// list with no flag that takes its value as the next word.
	recordable bool
	race       bool
	module     string
	patterns   []string
	with       func(subset []string) []string
	// groups is empty for a run that stays one command.
	groups              [][]splitItem
	recorded, defaulted int
	defaultSecs         float64
	perRun, overall     time.Duration
	parallel            int
}

// PlanGoTestRun plans r, run from root, against what this box has recorded of
// its packages. perRun is the budget one run may have.
func PlanGoTestRun(r Runner, root string, perRun time.Duration) GoTestPlan {
	if !isGoTestInvocation(r.Cmd, r.Args) {
		return GoTestPlan{}
	}
	dir := root
	if r.Dir != "" {
		dir = r.Dir
	}
	return newGoTestPlan(r, modulePathOf(dir), recordedPkgSecs(hasRaceFlag(r)), perRun)
}

// newGoTestPlan is PlanGoTestRun over estimates it is handed: the recorded
// seconds of each package by import path.
func newGoTestPlan(r Runner, module string, est map[string]float64, perRun time.Duration) GoTestPlan {
	p := GoTestPlan{race: hasRaceFlag(r), module: module, perRun: perRun, defaultSecs: unknownPlainPkgSecs}
	if p.race {
		p.defaultSecs = unknownRacePkgSecs
	}
	pkgs, with := argvbatch.GoTestPackages(r.Cmd, r.Args)
	if pkgs == nil {
		return p
	}
	p.recordable, p.patterns, p.with = true, pkgs, with
	if len(pkgs) < 2 {
		return p
	}
	items := make([]splitItem, len(pkgs))
	for i, pat := range pkgs {
		secs, ok := est[importPathOf(module, pat)]
		if ok {
			p.recorded++
		} else {
			secs = p.defaultSecs
			p.defaulted++
		}
		items[i] = splitItem{Pkg: pat, Cost: secs * suiteFloorMargin}
	}
	if groups := packGroups(items, perRun.Seconds()); len(groups) >= 2 {
		p.groups = groups
		p.overall = splitOverallBudget()
		p.parallel = splitParallelism(len(groups))
	}
	return p
}

// Split reports whether the plan cuts the list into several runs.
func (p GoTestPlan) Split() bool { return len(p.groups) >= 2 }

// Budget is the ceiling the stage holds the whole run under: the overall cap
// for a split plan, each run keeping its own per-run budget inside it, and the
// stage's own budget for a plan that is the one run it was.
func (p GoTestPlan) Budget(stage time.Duration) time.Duration {
	if p.Split() {
		return p.overall
	}
	return stage
}

// Describe is the one line a split prints: what was cut, from what evidence.
func (p GoTestPlan) Describe() string {
	runs := make([]string, len(p.groups))
	for i, g := range p.groups {
		runs[i] = "[" + strings.Join(names(g), " ") + "]"
	}
	return fmt.Sprintf("split into %d runs — %d packages (%d recorded, %d at the %.0fs default), each run weighed within %.0fs, %d at a time, %s overall: %s",
		len(p.groups), len(p.patterns), p.recorded, p.defaulted, p.defaultSecs, p.perRun.Seconds(), p.parallel, p.overall, strings.Join(runs, " "))
}

// splitOverallBudget is the overall cap on a split list: the operator's whole
// number of seconds, else the default. Anything that is not a positive whole
// number keeps the default: a mistyped cap must never become an instant
// timeout.
func splitOverallBudget() time.Duration {
	return time.Duration(config.Box().Positive("budgets.mech_total_s")) * time.Second
}

// splitParallelism is how many runs go side by side: the build-slot count
// unless the operator names another, never more than there are runs.
func splitParallelism(runs int) int {
	n := buildSlotCount()
	if v := config.Box().Positive("box.mech_parallel"); v >= 1 {
		n = v
	}
	return max(1, min(n, runs))
}

// importPathOf is the import path a package pattern names inside module, or
// "" for a pattern that names no one package (a wildcard) or a module not
// known.
func importPathOf(module, pattern string) string {
	if module == "" || strings.Contains(pattern, "...") {
		return ""
	}
	if pattern == "." {
		return module
	}
	return module + "/" + path.Clean(strings.TrimPrefix(pattern, "./"))
}

// modulePathOf reads the module path out of dir's go.mod; "" when there is no
// readable one.
func modulePathOf(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(data)) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest, _, _ = strings.Cut(rest, "//")
		return strings.Trim(strings.TrimSpace(rest), "\"`")
	}
	return ""
}
