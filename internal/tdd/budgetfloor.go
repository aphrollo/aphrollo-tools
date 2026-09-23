package tdd

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// What the box's load may decide, and what it may not.
//
// A suite stage's budget is the stage budget MINUS however long the run
// queued for a build slot (runCargoLocked sets Runner.Deadline before the
// wait, so the wait carves out of the run instead of stacking on top of it).
// That makes the budget a function of how busy the box is, and issue #660 is
// what that costs: one suite, four attempts on a loaded box, handed 441s,
// 96s, 148s and 335s. The same suite completes in ~350s here — it did on
// five merges the same night (352.3s, 357.0s, 429.1s, 431.3s, 261.5s) — so
// three of those four attempts were refused by a budget that was already
// below the work before the suite started. Each refusal spent a full run to
// prove nothing, and retrying does not converge: more retries mean more
// load, which means a smaller budget.
//
// The load reading keeps its job — deciding whether to START work, which is
// what the build slot is for — and loses the one it was never evidence
// for: how long the work TAKES. That question already has evidence on disk.
// gate.log records every run's stage, command and duration, so the budget is
// floored at what this same stage running this same command is recorded to
// need, and the load may only ever shrink it down to that floor.
//
// Three rules keep the floor honest:
//
//	no record, no floor. A repo or a suite gate.log has never seen a
//	   completed run of falls back to exactly today's arithmetic. A budget
//	   derived from evidence that does not exist must never be the reason a
//	   first run cannot happen.
//	only a run that FINISHED is evidence. A timeout's recorded duration is
//	   the budget it was cut off at, not the work; a cache hit measured
//	   nothing. Both are excluded, or the floor would learn the very number
//	   that is too small.
//	the floor never removes the ceiling. It is capped at the stage budget
//	   (cappedFloor), so a suite that genuinely hangs is still cut off and
//	   still reported as inconclusive — nothing was tested, and the merge
//	   does not land.

const (
	// suiteFloorMargin is the headroom over the recorded statistic. A run
	// refused at its own recorded duration would be a coin flip, and the
	// record is itself bounded by whatever budget produced it, so the
	// margin is what lets a slower-than-usual run finish rather than
	// arriving one second short.
	suiteFloorMargin = 1.5
	// suiteFloorWindow is how far back the evidence may come from: recent
	// enough that a suite which has since doubled in size is not budgeted
	// from its old shape, long enough that a repo committed to weekly still
	// has a record.
	suiteFloorWindow = 30 * 24 * time.Hour
	// suiteFloorSamples bounds the statistic to the most recent runs, so a
	// suite that got slower is budgeted from what it costs now.
	suiteFloorSamples = 20
)

// suiteFloor is the answer: the budget floor derived for one stage and
// command, and the evidence it came from, which the refusal quotes rather
// than asserting a number nobody can check.
type suiteFloor struct {
	// Budget is the floor itself, zero when there is no record to derive
	// one from. Read through cappedFloor at the point of use — on its own
	// it is what the evidence asks for, not what a run may have.
	Budget time.Duration
	// StatSecs is the statistic before the margin, in seconds, as gate.log
	// recorded it.
	StatSecs float64
	// Runs is how many completed runs the statistic was taken over.
	Runs int
}

// cappedFloor is what a run may actually have: the floor, never more than
// the stage budget. This is the one place requirement "a timeout must still
// be possible" is enforced — a floor is allowed to ask for more than the
// stage budget (the margin over a slow record routinely does), and is never
// granted it.
func cappedFloor(floor, stageBudget time.Duration) time.Duration {
	if floor > stageBudget {
		return stageBudget
	}
	return floor
}

// RefusalNote is the budget paragraph a timeout refusal carries: which floor
// the run got, what evidence set it, and what a retry can and cannot change.
// The old refusal said "The gate target is now warm; retry the commit",
// which under sustained load promises an improvement the next (smaller)
// budget cannot deliver.
func (f suiteFloor) RefusalNote(stageBudget time.Duration) string {
	if f.Budget <= 0 {
		return fmt.Sprintf("floor: none — gate.log holds no completed run of this command at this stage, so the %.0fs stage budget stood as configured. The first run that finishes records one.",
			stageBudget.Seconds())
	}
	capped := ""
	if f.Budget > stageBudget {
		capped = fmt.Sprintf(", capped at the %.0fs stage budget", stageBudget.Seconds())
	}
	return fmt.Sprintf("floor: %.0fs — this suite's own record in gate.log (p90 %.1fs over %d completed run%s, ×%.2g margin)%s. "+
		"The run above already had at least that long, so an immediate retry gets the same budget and not a bigger one: free the box, or make the suite smaller.",
		cappedFloor(f.Budget, stageBudget).Seconds(), f.StatSecs, f.Runs, plural(f.Runs), suiteFloorMargin, capped)
}

// recordedSuiteFloor derives the floor for one stage and command from
// gate.log. stage is the caller's own name for the stage (the gate name it
// logs under); the lookup maps it exactly the way appendGateLog does, so a
// name that is written one way and read another cannot silently match
// nothing.
//
// Deliberately NOT keyed on the root: the merge gate judges the merged tree
// in a throwaway temp checkout (prGateMergedCheckout), so every one of its
// runs is recorded under a path that never appears again — a root-keyed
// lookup would find no record precisely where #660 bites. The command is
// what identifies the work.
func recordedSuiteFloor(stage, cmd string) suiteFloor {
	return suiteFloorFrom(recordedSuiteSecs(gateLogStageToken(stage), cmd, suiteFloorWindow))
}

// recordedSuiteSecs is every completed run of this stage and command in
// gate.log within window, in log order (which is append order, so the tail
// is the most recent), bounded to the last suiteFloorSamples of them. A line
// this reader cannot parse is skipped rather than guessed at, exactly as
// GateStats treats one: the log is append-only text written by several
// processes.
func recordedSuiteSecs(stage, cmd string, window time.Duration) []float64 {
	dir := StateDir()
	if dir == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return nil
	}
	defer f.Close()
	now := time.Now()
	var secs []float64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || e.Stage != stage || e.Cmd != cmd {
			continue
		}
		// A run that never finished, or never ran, is not evidence about
		// how long the work takes — see isSettledVerdict. A zero duration
		// is a stage that recorded no stopwatch at all.
		if !isSettledVerdict(e.Verdict) || e.Secs <= 0 || now.Sub(e.At) > window {
			continue
		}
		secs = append(secs, e.Secs)
	}
	if len(secs) > suiteFloorSamples {
		secs = secs[len(secs)-suiteFloorSamples:]
	}
	return secs
}

// suiteFloorFrom is the statistic, chosen deliberately: the nearest-rank
// p90 of the recorded durations, plus suiteFloorMargin.
//
// A MAX is brittle — one bad night on a box that was swapping sets the floor
// for every run after it. A MEDIAN is blind to the slow tail, which is
// exactly when this bites: the five runs behind #660 have a median of 357s
// and a tail at 431s, and a budget floored at the median would still be
// below the work on the runs that actually time out. The p90 tracks the tail
// while needing more than one slow run to move, and the cap at the stage
// budget (cappedFloor) bounds how far a genuine outlier can push it — so the
// remaining brittleness can only ever cost a run a longer budget it will not
// use.
func suiteFloorFrom(secs []float64) suiteFloor {
	if len(secs) == 0 {
		return suiteFloor{}
	}
	sorted := append([]float64(nil), secs...)
	sort.Float64s(sorted)
	stat := sorted[nearestRankIndex(len(sorted), 0.9)]
	return suiteFloor{
		Budget:   time.Duration(math.Round(stat*suiteFloorMargin)) * time.Second,
		StatSecs: stat,
		Runs:     len(secs),
	}
}

// nearestRankIndex is the index of the p-th percentile of n sorted samples
// by the nearest-rank definition (ceil(p*n), 1-based), clamped into range.
// No interpolation: the answer is always one of the durations this box
// actually measured, which is what makes the refusal's "p90 431.3s" a
// quotable fact rather than a derived number nobody can find in the log.
func nearestRankIndex(n int, p float64) int {
	i := int(math.Ceil(p*float64(n))) - 1
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}
