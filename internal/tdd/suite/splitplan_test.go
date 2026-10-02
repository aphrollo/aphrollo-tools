package suite

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const planModule = "example.com/m"

// patterns is n package patterns ./p00 ./p01 ... in order.
func patterns(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("./p%02d", i)
	}
	return out
}

func raceRunner(pkgs ...string) Runner {
	return Runner{Cmd: "go", Args: append([]string{"test", "-race", "-count=1", "-shuffle=on"}, pkgs...)}
}

// planPatterns is every pattern the plan's runs name, flattened.
func planPatterns(p GoTestPlan) []string {
	var out []string
	for _, g := range p.groups {
		for _, it := range g {
			out = append(out, it.Pkg)
		}
	}
	return out
}

// TestNewGoTestPlan_TwentyPackagesWithNoRecordAreSplitByTheDefault pins the
// first run on a box that has recorded nothing: twenty unknown packages at the
// default cost (90s, weighed with the 1.5 margin to 135s each) do not fit one
// 600s run, so they are five runs of four, each package in exactly one.
func TestNewGoTestPlan_TwentyPackagesWithNoRecordAreSplitByTheDefault(t *testing.T) {
	pkgs := patterns(20)

	p := newGoTestPlan(raceRunner(pkgs...), planModule, nil, 600*time.Second)

	if !p.Split() || len(p.groups) != 5 {
		t.Fatalf("runs = %d (split %v), want 5 for 20 packages at 135s each in 600s runs", len(p.groups), p.Split())
	}
	for i, g := range p.groups {
		if len(g) != 4 {
			t.Errorf("run %d holds %d packages, want 4", i, len(g))
		}
	}
	// Packages of equal cost are dealt out in their own order, so the plan is
	// the same plan on every run: p00 p05 p10 p15 first, then p01 p06 ...
	for i, g := range p.groups {
		var want []string
		for j := i; j < 20; j += 5 {
			want = append(want, pkgs[j])
		}
		if !slices.Equal(names(g), want) {
			t.Errorf("run %d = %v, want %v", i, names(g), want)
		}
	}
	got := planPatterns(p)
	slices.Sort(got)
	if !slices.Equal(got, pkgs) {
		t.Fatalf("packages across runs = %v, want %v once each", got, pkgs)
	}
}

// TestNewGoTestPlan_APlainRunIsPlannedAtALowerDefaultThanARaceRun pins that
// a package nobody has timed costs a third as much without -race: the commit
// gate's plain run of thirteen strangers (30s, 45s weighed, 585s in all) is
// one run, and a fourteenth is what splits it, where the same thirteen under
// -race are four runs.
func TestNewGoTestPlan_APlainRunIsPlannedAtALowerDefaultThanARaceRun(t *testing.T) {
	plain := func(n int) Runner {
		return Runner{Cmd: "go", Args: append([]string{"test", "-count=1", "-shuffle=on"}, patterns(n)...)}
	}

	if p := newGoTestPlan(plain(13), planModule, nil, 600*time.Second); p.Split() {
		t.Errorf("13 plain packages split into %d runs, want one: 13 x 45s weighed is 585s of 600s", len(p.groups))
	}
	if p := newGoTestPlan(plain(14), planModule, nil, 600*time.Second); !p.Split() {
		t.Errorf("14 plain packages stayed one run, want a split: 630s weighed passes the 600s")
	}
	if p := newGoTestPlan(raceRunner(patterns(13)...), planModule, nil, 600*time.Second); len(p.groups) != 4 {
		t.Errorf("13 race packages = %d runs, want 4: 13 x 135s weighed is at most 4 to a run", len(p.groups))
	}
}

// TestNewGoTestPlan_APlanThatFitsIsNotSplit pins that a run the records say
// fits one budget stays the one run it was: three packages at 10s recorded
// are 45s of work in a 600s run.
func TestNewGoTestPlan_APlanThatFitsIsNotSplit(t *testing.T) {
	est := map[string]float64{planModule + "/p00": 10, planModule + "/p01": 10, planModule + "/p02": 10}

	p := newGoTestPlan(raceRunner(patterns(3)...), planModule, est, 600*time.Second)

	if p.Split() {
		t.Fatalf("plan split into %d runs, want the one run it was", len(p.groups))
	}
}

// TestNewGoTestPlan_ARecordedCostReplacesTheDefault pins the loop that makes
// the split improve: two packages fit one run at the default, but the record
// says p00 takes 400s (600s weighed), so the pair no longer fits and p00 runs
// alone.
func TestNewGoTestPlan_ARecordedCostReplacesTheDefault(t *testing.T) {
	est := map[string]float64{planModule + "/p00": 400}

	p := newGoTestPlan(raceRunner(patterns(2)...), planModule, est, 600*time.Second)

	if !p.Split() || len(p.groups) != 2 {
		t.Fatalf("runs = %d, want 2", len(p.groups))
	}
	if got := names(p.groups[0]); !slices.Equal(got, []string{"./p00"}) {
		t.Fatalf("first run = %v, want the recorded-heavy package alone", got)
	}
}

// TestNewGoTestPlan_OnePackageIsNeverSplit pins that a lone package, however
// dear its record, is the one run it was: there is nothing to cut.
func TestNewGoTestPlan_OnePackageIsNeverSplit(t *testing.T) {
	est := map[string]float64{planModule + "/p00": 5000}

	p := newGoTestPlan(raceRunner("./p00"), planModule, est, 600*time.Second)

	if p.Split() {
		t.Fatalf("a single package was split into %d runs", len(p.groups))
	}
}

// TestNewGoTestPlan_ALineItCannotRebuildIsLeftAlone pins the refusal: a run
// with a flag whose value is the next word (`-run X`) is never cut up, and
// its seconds are never recorded, since a filtered run's package time is not
// the package's.
func TestNewGoTestPlan_ALineItCannotRebuildIsLeftAlone(t *testing.T) {
	r := Runner{Cmd: "go", Args: append([]string{"test", "-run", "TestX"}, patterns(20)...)}

	p := newGoTestPlan(r, planModule, nil, 600*time.Second)

	if p.Split() || p.recordable {
		t.Fatalf("plan for a `-run X` line: split %v, recordable %v, want neither", p.Split(), p.recordable)
	}
}

// TestNewGoTestPlan_ANonGoRunIsLeftAlone pins that only `go test` is planned.
func TestNewGoTestPlan_ANonGoRunIsLeftAlone(t *testing.T) {
	p := newGoTestPlan(Runner{Cmd: "cargo", Args: []string{"test", "-p", "a", "-p", "b"}}, planModule, nil, 600*time.Second)

	if p.Split() || p.recordable {
		t.Fatalf("plan for a cargo run: split %v, recordable %v, want neither", p.Split(), p.recordable)
	}
}

// TestNewGoTestPlan_ABudgetThatFitsEveryPackageButNotTheSumSplits pins that
// the sum is what is held to the run budget, not the largest package: two
// packages of 200s (300s weighed) each fit a 600s run alone, and 600s weighed
// together is exactly the budget, so the pair stays one run, while one second
// more per package splits it.
func TestNewGoTestPlan_ABudgetThatFitsEveryPackageButNotTheSumSplits(t *testing.T) {
	two := func(secs float64) GoTestPlan {
		est := map[string]float64{planModule + "/p00": secs, planModule + "/p01": secs}
		return newGoTestPlan(raceRunner(patterns(2)...), planModule, est, 600*time.Second)
	}

	if p := two(200); p.Split() {
		t.Errorf("two packages of 200s split into %d runs, want one: 600s weighed is the whole 600s budget", len(p.groups))
	}
	if p := two(201); !p.Split() || len(p.groups) != 2 {
		t.Errorf("two packages of 201s: split %v, want two runs: 603s weighed passes the 600s budget", p.Split())
	}
}

// TestGoTestPlan_BudgetIsTheOverallCapOnlyWhenSplit pins which ceiling a
// stage runs under: the overall cap for a split plan (each run holds its own
// per-run budget inside it), and the stage budget it always had for a plan
// that is one run.
func TestGoTestPlan_BudgetIsTheOverallCapOnlyWhenSplit(t *testing.T) {
	t.Setenv("APHROLLO_MECH_TOTAL_SECS", "1800")
	split := newGoTestPlan(raceRunner(patterns(20)...), planModule, nil, 600*time.Second)
	one := newGoTestPlan(raceRunner(patterns(2)...), planModule, nil, 600*time.Second)

	if got := split.Budget(600 * time.Second); got != 1800*time.Second {
		t.Errorf("split plan budget = %s, want the 30m overall cap", got)
	}
	if got := one.Budget(600 * time.Second); got != 600*time.Second {
		t.Errorf("one-run plan budget = %s, want the stage budget", got)
	}
}

// TestSplitOverallBudget_IsConfigurableAndRefusesJunk pins the knob: a whole
// number of seconds is taken, and anything else (junk, zero, negative) keeps
// the default 45 minutes, since a mistyped cap must never become an instant
// timeout.
func TestSplitOverallBudget_IsConfigurableAndRefusesJunk(t *testing.T) {
	cases := map[string]time.Duration{
		"":     45 * time.Minute,
		"900":  900 * time.Second,
		"junk": 45 * time.Minute,
		"0":    45 * time.Minute,
		"-5":   45 * time.Minute,
	}
	for raw, want := range cases {
		t.Setenv("APHROLLO_MECH_TOTAL_SECS", raw)
		if got := splitOverallBudget(); got != want {
			t.Errorf("APHROLLO_MECH_TOTAL_SECS=%q → %s, want %s", raw, got, want)
		}
	}
}

// TestSplitParallelism_FollowsTheBuildSlotsAndTheKnobNeverPastTheRuns pins the
// width: the number of build slots by default, the operator's number when
// given, never more than there are runs, never below one.
func TestSplitParallelism_FollowsTheBuildSlotsAndTheKnobNeverPastTheRuns(t *testing.T) {
	t.Setenv("APHROLLO_BUILD_SLOTS", "3")
	t.Setenv("APHROLLO_MECH_PARALLEL", "")
	if got := splitParallelism(8); got != 3 {
		t.Errorf("default width with 3 slots and 8 runs = %d, want 3", got)
	}
	if got := splitParallelism(2); got != 2 {
		t.Errorf("width with 3 slots and 2 runs = %d, want 2", got)
	}
	t.Setenv("APHROLLO_MECH_PARALLEL", "1")
	if got := splitParallelism(8); got != 1 {
		t.Errorf("width with APHROLLO_MECH_PARALLEL=1 = %d, want 1", got)
	}
	t.Setenv("APHROLLO_MECH_PARALLEL", "0")
	if got := splitParallelism(8); got != 3 {
		t.Errorf("width with APHROLLO_MECH_PARALLEL=0 = %d, want the slot default 3", got)
	}
}

// TestGoTestPlan_DescribeNamesTheRunsAndWhatTheyWereJudgedFrom pins the line a
// split prints: how many packages, how many came from a record, and the runs.
func TestGoTestPlan_DescribeNamesTheRunsAndWhatTheyWereJudgedFrom(t *testing.T) {
	est := map[string]float64{planModule + "/p00": 400}
	p := newGoTestPlan(raceRunner(patterns(2)...), planModule, est, 600*time.Second)

	got := p.Describe()

	for _, want := range []string{"2 runs", "2 packages (1 recorded, 1 at the 90s default)", "./p00", "./p01"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, want it to contain %q", got, want)
		}
	}
}

// TestImportPathOf_NamesThePackageAPatternMeans pins the pattern to import
// path mapping the record is keyed by: the root package is the module, a
// directory is the module plus its path, and a wildcard or an unknown module
// names no one package.
func TestImportPathOf_NamesThePackageAPatternMeans(t *testing.T) {
	cases := []struct{ module, pattern, want string }{
		{"example.com/m", ".", "example.com/m"},
		{"example.com/m", "./internal/cli", "example.com/m/internal/cli"},
		{"example.com/m", "./a/../b", "example.com/m/b"},
		{"example.com/m", "./internal/...", ""},
		{"example.com/m", "./...", ""},
		{"", "./a", ""},
	}
	for _, c := range cases {
		if got := importPathOf(c.module, c.pattern); got != c.want {
			t.Errorf("importPathOf(%q, %q) = %q, want %q", c.module, c.pattern, got, c.want)
		}
	}
}

// TestModulePathOf_ReadsTheModuleLine pins the one thing the plan needs from
// go.mod, and that a missing file is no module rather than a failure.
func TestModulePathOf_ReadsTheModuleLine(t *testing.T) {
	dir := t.TempDir()
	if got := modulePathOf(dir); got != "" {
		t.Errorf("module of a directory with no go.mod = %q, want none", got)
	}
	write(t, dir, "go.mod", "// c\nmoduleish x\nmodule\n\nmodule\t\"example.com/q\" // trailing\n\ngo 1.26\n")

	if got := modulePathOf(dir); got != "example.com/q" {
		t.Fatalf("module = %q, want example.com/q: a word that only starts with module, and a bare module, are not the line", got)
	}
}
