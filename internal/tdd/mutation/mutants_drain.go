package mutation

import (
	"context"
	"io"
	"time"
)

// A retry that starts the moment the box killed the first attempt runs into
// the same wall.
//
// The retry in mutants_envfail.go was written for a shard the RUN's own
// siblings had crowded out: every other shard has finished by the time it
// runs, so the machine is quiet again and one narrow attempt is enough. That
// is not the case this file exists for. On the run that prompted it the
// pressure came from OUTSIDE the run — three other sessions with 25 cargo and
// rustc processes compiling — and finishing the run's own shards freed
// nothing. Six baselines died of `rustc-LLVM ERROR: out of memory` and a
// failed 2.6 MB allocation, and an immediate retry at the same width is one
// more attempt into a box that is still full.
//
// So the retry waits for room, bounded, out loud:
//
//	room means the retry's OWN budget — the cold jobs it is about to start,
//	   priced at what this package already prices a cold job at. Not a
//	   constant, and not "some", so the wait ends when the thing being waited
//	   for is true rather than when a timer chosen by nobody expires.
//	bounded, because the work holding the box belongs to somebody else and
//	   may run for hours. On the timeout the retry goes ahead at the narrowest
//	   width that still builds: a measurement attempted slowly is worth more
//	   than one not attempted, and a second environmental death is already
//	   refused as unmeasured rather than counted as caught, so waiting can
//	   never buy a false green.
//	out loud, both the waiting and the not-waiting. A silent wait is
//	   indistinguishable from a hang, and an unreadable box that skipped the
//	   wait has to say so or the log implies a check that never happened.

const (
	// mutantsDrainTimeout is the whole wait's budget. Long enough for
	// another session's cargo to finish a crate or two and give its memory
	// back, short enough that a shard's retry cannot outlast the patience of
	// whoever is watching the merge.
	mutantsDrainTimeout = 10 * time.Minute
	// mutantsDrainPoll is how often the box is asked again. Cheap — one
	// GlobalMemoryStatusEx or one read of /proc/meminfo — so the interval is
	// about not spamming the log rather than about cost.
	mutantsDrainPoll = 15 * time.Second
)

// mutantsDrainWaitFn is one poll interval, a seam so a test can prove the
// waiting without paying for it. false means the caller's context is done and
// the wait must stop.
var mutantsDrainWaitFn = func(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// waitForRoomToRetry holds a killed shard's retry until the box has room for
// the jobs it is about to start, and answers the width to retry at: the one
// asked for when the room appeared, and the floor of 1 when it did not.
//
// An unreadable free-memory reading is UNKNOWN and never a wait — the same
// rule the budgets themselves follow (mutants_freemem.go). Ten minutes bought
// on a box this side cannot see is ten minutes bought for nothing.
func waitForRoomToRetry(ctx context.Context, jobs, shard int, log io.Writer) int {
	if jobs < 1 {
		jobs = 1
	}
	needGB := jobs * mutantsRAMGBPerColdBuildJob
	availGB := mutantsFreeMemoryGB()
	if availGB <= 0 {
		logf(log, "mutants: shard %d's retry wants %d GB for %d cold build job%s, and this box's free memory "+
			"could not be read — retrying now, as before", shard, needGB, jobs, plural(jobs))
		return jobs
	}
	if availGB >= needGB {
		return jobs
	}
	logf(log, "mutants: shard %d's retry wants %d GB for %d cold build job%s and the box has %d GB free — "+
		"waiting up to %s for it to drain, because retrying now runs into the wall that killed it",
		shard, needGB, jobs, plural(jobs), availGB, mutantsDrainTimeout)
	start := time.Now()
	// Bounded by the number of polls rather than by a wall-clock deadline:
	// the wait itself is the seam a test replaces, and a deadline read off
	// the clock would never expire once it does not really sleep.
	for range int(mutantsDrainTimeout / mutantsDrainPoll) {
		if !mutantsDrainWaitFn(ctx, mutantsDrainPoll) {
			logf(log, "mutants: shard %d's wait for the box was cancelled after %s", shard, since(start))
			return 1
		}
		if availGB = mutantsFreeMemoryGB(); availGB >= needGB {
			logf(log, "mutants: the box drained to %d GB free after %s — retrying shard %d at %d build job%s",
				availGB, since(start), shard, jobs, plural(jobs))
			return jobs
		}
	}
	logf(log, "mutants: the box still has only %d GB free after %s — retrying shard %d at 1 build job instead "+
		"of %d rather than not at all; if it dies again the run reports it as unmeasured, never as caught",
		availGB, mutantsDrainTimeout, shard, jobs)
	return 1
}

// mutantsFreeMemoryGB is the box's free-memory reading alone, through the
// same seam every other budget reads it through, so a test pins one box
// rather than two.
func mutantsFreeMemoryGB() int {
	_, _, availGB := mutantsBoxShapeFn()
	return availGB
}

// since is the elapsed time a log line reports, rounded to the second: a wait
// measured to the nanosecond reads like precision nobody has.
func since(start time.Time) time.Duration {
	return time.Since(start).Round(time.Second)
}
