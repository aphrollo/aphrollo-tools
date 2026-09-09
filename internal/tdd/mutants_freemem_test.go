package tdd

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

// The budget assumed it owned the box. It does not.
//
// Measured on the 24-core, 63 GB box (fixed 16 GB pagefile, so the hard
// machine-wide ceiling is about 79 GB of commit charge, not 63 GB of RAM)
// while a borld merge ran beside three other sessions' builds: 25 cargo and
// rustc processes were already compiling when the run started, and the
// derivation still read `ram 63GB/8=7` and started seven shards. Six of the
// seven baselines then died of the machine rather than of the lane —
// `rustc-LLVM ERROR: out of memory` three times, and `memory allocation of
// 2654224 bytes failed` with an ICE behind it — after 1018-1115 s of work
// that measured nothing.
//
// Total RAM is what the box HAS; it is not what this run may have. The number
// that bounds it is available commit, which GlobalMemoryStatusEx already
// carried in the field machineRAMGB was discarding, and MemAvailable is its
// Linux equivalent.
func TestMutantsJobsCap_BudgetsOnFreeMemoryRatherThanTotalRAM(t *testing.T) {
	t.Parallel()
	idle, idleWhy := MutantsJobsCap(24, 63, 0)
	busy, busyWhy := MutantsJobsCap(24, 63, 24)

	if want := "min(cores 24/3=8, ram 63GB/8=7, cap 8) — ram"; idle != 7 || idleWhy != want {
		t.Errorf("unknown free memory = (%d, %q), want (7, %q) — today's answer, unchanged", idle, idleWhy, want)
	}
	if want := "min(cores 24/3=8, free 24GB/8=3 (measured), cap 8) — free"; busy != 3 || busyWhy != want {
		t.Errorf("24 GB free on the same 63 GB box = (%d, %q), want (3, %q)", busy, busyWhy, want)
	}
	if busy >= idle {
		t.Errorf("a busy box derived %d shards and an idle one %d, on the same total RAM — the reading "+
			"the whole change exists for does not reach the answer", busy, idle)
	}
	if !strings.Contains(busyWhy, "measured") {
		t.Errorf("reason = %q, want it to say the memory term was measured rather than assumed: a reader "+
			"has to tell a free-memory decision from a total-RAM one at a glance", busyWhy)
	}
}

// A reading that is BIGGER than total RAM is the ordinary case on an idle box
// with a pagefile — available commit is RAM plus pagefile minus what is
// charged. It must never widen the run: the memory term is the smaller of
// what the box has and what is free on it, so this change only ever lowers
// the numbers today's code derives.
func TestMutantsJobsCap_FreeMemoryNeverRaisesTheCountAboveTotalRAM(t *testing.T) {
	t.Parallel()
	roomy, why := MutantsJobsCap(24, 63, 200)
	unknown, _ := MutantsJobsCap(24, 63, 0)
	if roomy != unknown {
		t.Errorf("200 GB of free commit derived %d shards against total RAM's %d — free memory may lower "+
			"the count, never raise it", roomy, unknown)
	}
	if !strings.Contains(why, "— ram") {
		t.Errorf("reason = %q, want the RAM term named as the binding one", why)
	}
}

// Zero is UNKNOWN, not zero bytes: /proc/meminfo without MemAvailable, a
// GlobalMemoryStatusEx that failed, a unix with no procfs at all. An unknown
// reading falls back to today's behaviour and never to a guess — the same
// rule the RAM term itself already follows.
func TestMutantsBudget_UnknownFreeMemoryFallsBackToTotalRAM(t *testing.T) {
	t.Parallel()
	if gb, term := mutantsBudgetMemoryGB(63, 0); gb != 63 || term != "ram" {
		t.Errorf("mutantsBudgetMemoryGB(63, 0) = (%d, %q), want (63, \"ram\")", gb, term)
	}
	if gb, term := mutantsBudgetMemoryGB(63, 24); gb != 24 || term != "free" {
		t.Errorf("mutantsBudgetMemoryGB(63, 24) = (%d, %q), want (24, \"free\")", gb, term)
	}
	// Neither readable is the non-Linux unix: nothing constrains, and the
	// cores decide alone.
	if gb, _ := mutantsBudgetMemoryGB(0, 0); gb != 0 {
		t.Errorf("mutantsBudgetMemoryGB(0, 0) = %d, want 0 — unknown does not constrain", gb)
	}
	// RAM unreadable but the free reading present still binds: a number that
	// was measured is better than no number.
	if gb, term := mutantsBudgetMemoryGB(0, 12); gb != 12 || term != "free" {
		t.Errorf("mutantsBudgetMemoryGB(0, 12) = (%d, %q), want (12, \"free\")", gb, term)
	}
}

// The same reading has to reach the BUILD width, which is what actually
// starts rustc processes: seven shards each allowed to build wide is the
// multiplication that exhausted the box in the first place.
func TestMutantsBuildJobsCap_BudgetsOnFreeMemoryRatherThanTotalRAM(t *testing.T) {
	t.Parallel()
	idle, idleWhy := mutantsBuildJobsCap(24, 63, 0, 2, true)
	busy, busyWhy := mutantsBuildJobsCap(24, 63, 12, 2, true)

	if want := "min(cores 24, ram 63GB/6GB=10) — ram: 10 total across 2 shards, cold"; idle != 5 || idleWhy != want {
		t.Errorf("unknown free memory = (%d, %q), want (5, %q) — today's answer, unchanged", idle, idleWhy, want)
	}
	if want := "min(cores 24, free 12GB/6GB=2 (measured)) — free: 2 total across 2 shards, cold"; busy != 1 || busyWhy != want {
		t.Errorf("12 GB free on the same 63 GB box = (%d, %q), want (1, %q)", busy, busyWhy, want)
	}
	if busy >= idle {
		t.Errorf("a busy box built %d jobs wide per shard and an idle one %d — the free reading does not "+
			"reach the build width", busy, idle)
	}
}

// A busy box must degrade to a slow correct run, never to no work at all.
// Whatever the reading says, the run gets one shard and one build job.
func TestMutantsBudget_NeverDerivesFewerThanOneShardOrOneJob(t *testing.T) {
	t.Parallel()
	for _, availGB := range []int{1, 2, 7} {
		if shards, why := MutantsJobsCap(24, 63, availGB); shards < 1 {
			t.Errorf("%d GB free derived %d shards (%s), want the floor of 1", availGB, shards, why)
		}
		if jobs, why := mutantsBuildJobsCap(24, 63, availGB, 7, true); jobs < 1 {
			t.Errorf("%d GB free derived %d build jobs (%s), want the floor of 1", availGB, jobs, why)
		}
	}
	// And the floor is reported as a floor rather than presented as a budget
	// the box can keep, exactly as the shards-over-cores case already is.
	if _, why := mutantsBuildJobsCap(24, 63, 6, 7, true); !strings.Contains(why, "floored at 1 per shard") {
		t.Errorf("reason = %q, want it to say the floor decided", why)
	}
}

// Read ONCE per run, at the point the budget is computed. A reading taken
// again for each shard is a different number every time on a box that is
// moving, and two shards of one run would then disagree about the same
// machine — one building three jobs wide and its neighbour one, with nothing
// in the log to say why.
func TestMeasure_FreeMemoryIsReadOncePerRunNotOncePerShard(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	// A box draining fast: every read answers less than the one before, so a
	// per-shard read cannot produce three equal widths by luck.
	prev := mutantsBoxShapeFn
	var reads atomic.Int32
	mutantsBoxShapeFn = func() (int, int, int) {
		return 24, 63, 60 - 12*int(reads.Add(1))
	}
	t.Cleanup(func() { mutantsBoxShapeFn = prev })
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shardIndexOf(c.Argv), Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	widths := map[string]int{}
	for _, c := range *calls {
		widths[envValueOf(c.Env, "CARGO_BUILD_JOBS")]++
	}
	if len(widths) != 1 {
		t.Errorf("the run's shards built at %v — one run, one reading, one width", widths)
	}
}
