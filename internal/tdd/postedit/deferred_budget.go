package postedit

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// The ceiling a deferred run is abandoned at, and the -timeout a `go test` run
// is handed above it, used to be one flat 600s (budgets.deferred_max_s). A suite
// that takes 607s alone, or 1775s on a loaded box, could never end with a
// verdict: it was killed, and the CPU of a full run bought "no result".
//
// The budget is now sized from the command's own recorded durations, the same
// evidence the commit gate floors its suite budget on (lock/budgetfloor.go):
//
//	budget = clamp(p90 of recent runs × headroom × load factor, floor, cap)
//
// The floor is today's value, so a command with no record, or a fast one, keeps
// exactly what it had; the cap keeps a hung process reportable. A run that was
// abandoned counts in the record at the seconds it had spent: a lower bound, never
// the work, but without it a suite that never finishes would never be given more
// room to finish in. The factors are named constants below, as data.
const (
	// budgetHeadroom is the margin over the p90, so a slower-than-usual run
	// finishes instead of arriving a second short.
	budgetHeadroom = 1.5
	// budgetLoadWeight is what a fully busy box (100% CPU) adds to the load
	// factor: the factor is 1 + load × this, so 100% load doubles the budget.
	budgetLoadWeight = 1.0
	// budgetCap bounds how long a deferred run may ever be given, whatever the
	// record asks for. Never below the floor (see deferredBudgetCap).
	budgetCap = 60 * time.Minute
	// budgetSamples bounds the statistic to the most recent runs, and
	// budgetWindow how old a run may be and still count.
	budgetSamples = 20
	budgetWindow  = 30 * 24 * time.Hour
)

// budgetHistoryFn and budgetLoadFn are the two inputs, swapped in tests: the
// recorded seconds of a command, and the box's CPU load in percent.
var (
	budgetHistoryFn = recordedRunSecs
	budgetLoadFn    = BoxLoadPct
)

// deferredBudgetSizing is a budget and what it came from, so the line that
// reports a run without a verdict quotes the measurement rather than a number.
type deferredBudgetSizing struct {
	Budget time.Duration
	Floor  time.Duration
	// P90 is the measured statistic in seconds over Runs runs; zero Runs means
	// there was no record and the floor stood.
	P90  float64
	Runs int
	// Factor is the load factor applied; Asked what the record asked for.
	Factor float64
	Asked  time.Duration
}

// sizeDeferredBudget sizes the ceiling of a run of argv in root. The load is
// read only when the record asks for more than the floor: a floor answer needs
// no sample of the box.
func sizeDeferredBudget(root string, argv []string) deferredBudgetSizing {
	floor := deferredMax()
	s := deferredBudgetSizing{Budget: floor, Floor: floor, Factor: 1}
	secs := budgetHistoryFn(root, argv)
	if len(secs) == 0 {
		return s
	}
	sorted := slices.Clone(secs)
	sort.Float64s(sorted)
	s.Runs = len(sorted)
	s.P90 = sorted[int(math.Ceil(0.9*float64(len(sorted))))-1]
	s.Asked = time.Duration(math.Round(s.P90*budgetHeadroom)) * time.Second
	if s.Asked <= floor {
		return s
	}
	if pct, ok := budgetLoadFn(); ok && pct > 0 {
		s.Factor = 1 + pct/100*budgetLoadWeight
	}
	s.Asked = time.Duration(math.Round(s.P90*budgetHeadroom*s.Factor)) * time.Second
	s.Budget = min(max(s.Asked, floor), max(budgetCap, floor))
	return s
}

// Note is the one line that says where the budget came from.
func (s deferredBudgetSizing) Note() string {
	switch {
	case s.Runs == 0:
		return fmt.Sprintf("budget %.0fs, the floor: no recorded run of this command", s.Budget.Seconds())
	case s.Asked <= s.Floor && s.Budget == s.Floor:
		return fmt.Sprintf("budget %.0fs, the floor: measured p90 %.0fs × %g headroom is below it (%d runs)",
			s.Budget.Seconds(), s.P90, budgetHeadroom, s.Runs)
	case s.Asked > s.Budget:
		return fmt.Sprintf("budget %.0fs, the cap: measured p90 %.0fs × %g headroom × %.2f load asks for %.0fs",
			s.Budget.Seconds(), s.P90, budgetHeadroom, s.Factor, s.Asked.Seconds())
	}
	return fmt.Sprintf("budget %.0fs, from measured p90 %.0fs × %g headroom × %.2f load (%d runs)",
		s.Budget.Seconds(), s.P90, budgetHeadroom, s.Factor, s.Runs)
}

// runKey is the spelling of a run the record is kept under: the command without
// the -timeout and -count=1 a deferred phase adds, so the edit's own log line
// and the abandonment's (written from the phase's argv) are one command.
func runKey(argv []string) string {
	kept := make([]string, 0, len(argv))
	for _, a := range argv {
		if strings.HasPrefix(a, "-timeout=") || a == "-count=1" {
			continue
		}
		kept = append(kept, a)
	}
	return strings.Join(kept, " ")
}

// sameRunArgv reports whether two phase argv are one run, whatever -timeout
// each was given: the budget differs between two hooks that sized it at
// different loads, and that is not a different run.
func sameRunArgv(a, b []string) bool { return runKey(a) == runKey(b) }

// recordedRunSecs is the seconds of the latest runs of argv in this repo's
// event log: the green ones, and the abandoned ones at the seconds they had
// spent. A red is no evidence of how long the suite takes (it usually stopped at
// the first failure), and a marker that a run was deferred measured nothing.
func recordedRunSecs(root string, argv []string) []float64 {
	key := runKey(argv)
	now := time.Now()
	var secs []float64
	for _, e := range readGateEntries(root, now.Add(-budgetWindow)) {
		if e.Stage != "postedit" || e.Secs <= 0 || now.Sub(e.At) > budgetWindow {
			continue
		}
		if e.Verdict != "green" && e.Verdict != "green-with-warnings" && e.Verdict != DeferredAbandoned {
			continue
		}
		if runKey(strings.Fields(e.Cmd)) != key {
			continue
		}
		secs = append(secs, e.Secs)
	}
	if len(secs) > budgetSamples {
		secs = secs[len(secs)-budgetSamples:]
	}
	return secs
}

// deferredCeiling is the life a job was given: the budget it was sized to, or,
// on a record from before budgets, today's flat ceiling.
func deferredCeiling(j DeferredJob) time.Duration {
	if j.BudgetSecs > 0 {
		return time.Duration(j.BudgetSecs) * time.Second
	}
	return deferredMax()
}
