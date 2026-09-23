package mutation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The two ways the box killed six of seven baselines on the run this came
// from, neither of which any existing signature matched:
//
//	rustc-LLVM ERROR: out of memory
//	memory allocation of 2654224 bytes failed
//
// They are the CAUSE of the ICE and the metadata wreckage already listed —
// rustc aborts, and what the next compilation reads is what the aborted one
// half-wrote — so they are tried first, and a run carrying both is named by
// the exhaustion rather than by its consequence.
func TestMutantsEnvironmentalBuildFailure_NamesTheOutOfMemoryDeaths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, output string
		want         bool
	}{
		{name: "LLVM out of memory", want: true,
			output: "rustc-LLVM ERROR: out of memory\nerror: could not compile `bevy_input` (lib)\n"},
		{name: "a failed allocation with an ICE behind it", want: true,
			output: "memory allocation of 2654224 bytes failed\nerror: rustc interrupted by SIGABRT\n"},
		// Still the lane's: an out-of-memory string a TEST printed is a test
		// that printed a string, and this must not excuse it.
		{name: "a real compile error is the lane's", want: false,
			output: "error[E0308]: mismatched types\n  --> crates/a/src/lib.rs:1:36\n"},
	} {
		signature, got := mutantsEnvironmentalBuildFailure(c.output)
		if got != c.want {
			t.Errorf("%s: environmental = %v (%q), want %v", c.name, got, signature, c.want)
		}
		if got && !strings.Contains(signature, "memory") {
			t.Errorf("%s: signature = %q, want it to name the exhaustion an operator has to act on", c.name, signature)
		}
	}
}

// setBoxAvailSequenceForTest pins the box's free-memory reading to one value
// per call, holding the last one once the list runs out — a box draining, or
// a box that never does.
func setBoxAvailSequenceForTest(t *testing.T, avail ...int) {
	t.Helper()
	prev := mutantsBoxShapeFn
	i := 0
	mutantsBoxShapeFn = func() (int, int, int) {
		got := avail[min(i, len(avail)-1)]
		i++
		return 24, 63, got
	}
	t.Cleanup(func() { mutantsBoxShapeFn = prev })
}

// countDrainWaitsForTest stands the wait itself down and counts it, so a test
// proves the waiting without paying for it.
func countDrainWaitsForTest(t *testing.T) *int {
	t.Helper()
	prev := mutantsDrainWaitFn
	waits := 0
	mutantsDrainWaitFn = func(context.Context, time.Duration) bool {
		waits++
		return true
	}
	t.Cleanup(func() { mutantsDrainWaitFn = prev })
	return &waits
}

// Retrying at the same width the moment the box killed the first attempt runs
// straight back into the wall that killed it: the machine is still holding
// everything it was holding. The retry waits until there is room for the
// budget it is about to ask for, and says what it is waiting for — a silent
// wait is indistinguishable from a hang.
func TestRetryDrain_WaitsUntilTheBoxHasRoomForTheRetrysOwnBudget(t *testing.T) {
	setBoxAvailSequenceForTest(t, 6, 6, 60)
	waits := countDrainWaitsForTest(t)
	var log strings.Builder

	jobs := waitForRoomToRetry(t.Context(), 5, 1, &log)

	if jobs != 5 {
		t.Errorf("retry width = %d, want the full 5 once the box had room for it", jobs)
	}
	if *waits != 2 {
		t.Errorf("waited %d time(s), want two polls before the third reading answered", *waits)
	}
	got := log.String()
	// 5 cold jobs at mutantsRAMGBPerColdBuildJob is 30 GB, and 6 GB is what
	// the box had — both numbers, or nobody can tell a wait that was right
	// from one that was superstition.
	for _, want := range []string{"30 GB", "6 GB", "shard 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("log = %q, want it to name %q", got, want)
		}
	}
}

// Bounded. A box that never drains — because the work holding it is somebody
// else's hours-long build — must not turn one shard's retry into a run that
// hangs until a human kills it. On the timeout the retry goes ahead at the
// narrowest width that still builds, because a measurement attempted slowly
// is worth more than one not attempted, and a second environmental death is
// already refused as unmeasured rather than counted as caught.
func TestRetryDrain_DoesNotWaitForeverAndRetriesNarrowerInstead(t *testing.T) {
	setBoxAvailSequenceForTest(t, 4)
	waits := countDrainWaitsForTest(t)
	var log strings.Builder

	jobs := waitForRoomToRetry(t.Context(), 8, 2, &log)

	if jobs != 1 {
		t.Errorf("retry width = %d after the wait timed out, want the floor of 1 — narrower, never nothing", jobs)
	}
	if want := int(mutantsDrainTimeout / mutantsDrainPoll); *waits != want {
		t.Errorf("waited %d time(s), want exactly %d — %s of polling and then a decision", *waits, want, mutantsDrainTimeout)
	}
	if got := log.String(); !strings.Contains(got, mutantsDrainTimeout.String()) || !strings.Contains(got, "1") {
		t.Errorf("log = %q, want it to say how long it waited and what it decided to do instead", got)
	}
}

// A reading that could not be taken is UNKNOWN, and unknown may not become a
// wait: a box whose free memory this side cannot see would hold every retry
// for the full timeout and then run anyway, which is ten minutes bought for
// nothing. It falls back to today's behaviour — retry now, at the derived
// width — and says that is what it did.
func TestRetryDrain_UnreadableFreeMemoryRetriesNowRatherThanWaiting(t *testing.T) {
	setBoxAvailSequenceForTest(t, 0)
	waits := countDrainWaitsForTest(t)
	var log strings.Builder

	jobs := waitForRoomToRetry(t.Context(), 4, 0, &log)

	if jobs != 4 {
		t.Errorf("retry width = %d, want the derived 4 — an unknown reading constrains nothing", jobs)
	}
	if *waits != 0 {
		t.Errorf("waited %d time(s) on a box it cannot read, want none", *waits)
	}
	if got := log.String(); !strings.Contains(got, "could not be read") {
		t.Errorf("log = %q, want it to say the reading could not be taken", got)
	}
}

// A box with room already is the ordinary case — the other shards have
// finished and released what they held — and it must not pay a poll interval
// to discover it.
func TestRetryDrain_DoesNotWaitWhenTheBoxAlreadyHasRoom(t *testing.T) {
	setBoxAvailSequenceForTest(t, 48)
	waits := countDrainWaitsForTest(t)
	var log strings.Builder

	if jobs := waitForRoomToRetry(t.Context(), 4, 0, &log); jobs != 4 {
		t.Errorf("retry width = %d, want the derived 4", jobs)
	}
	if *waits != 0 {
		t.Errorf("waited %d time(s) on a box with room, want none", *waits)
	}
}

// End to end: the shard the box killed with an out-of-memory abort is the one
// the wait exists for. It waits, it says so in the run's own log, and the
// verdict it reaches afterwards is a measurement — never a green bought by
// skipping the retry.
func TestMeasure_OutOfMemoryShardWaitsForTheBoxBeforeItsRetry(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	// 4 GB free and staying there: less than one cold job's 6 GB, so the
	// retry waits out its whole budget and then goes ahead narrow.
	setBoxAvailSequenceForTest(t, 4)
	countDrainWaitsForTest(t)
	// The shards run concurrently, so the per-shard attempt count they share
	// is written from several goroutines at once — the race detector fails
	// the test on the unguarded map, and the count it reads decides which
	// call fails, so a torn read would also make the fixture itself flaky.
	var mu sync.Mutex
	attempts := map[int]int{}
	nthAttempt := func(shard int) int {
		mu.Lock()
		defer mu.Unlock()
		attempts[shard]++
		return attempts[shard]
	}
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		nth := nthAttempt(shard)
		if shard == 1 && nth == 1 {
			mustWrite(t, filepath.Join(mutantsShardTargetDir(root, 1), "debug", "deps", "libcore.rmeta"), "half\n")
			fmt.Fprint(c.Log, "FAILED   Unmutated baseline in 1018s build\nrustc-LLVM ERROR: out of memory\n")
			return 4, nil
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shard, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(*calls) != 4 {
		t.Fatalf("ran the tool %d time(s), want three shards and one retry", len(*calls))
	}
	if got := log.String(); !strings.Contains(got, "out of memory") || !strings.Contains(got, "waiting") {
		t.Errorf("log = %q, want the box's own failure named and the wait it caused", got)
	}
	if v.Refused || v.Tested != 3 {
		t.Fatalf("verdict = %+v, want all three shards measured once the retry answered", v)
	}
	if _, err := os.Stat(filepath.Join(mutantsShardTargetDir(root, 1), "debug", "deps", "libcore.rmeta")); err == nil {
		t.Errorf("the poisoned build dir survived into the retry")
	}
}
