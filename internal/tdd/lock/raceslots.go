package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// How many `go test -race` runs the box carries at once.
//
// Every race run used to take ONE key (goRaceLockKey), so four lanes merging
// together ran strictly one after another: the tail of a queue of four waited
// 33 minutes on a box with room for two of them (#1172). The key is now a
// family of them, one per concurrent run the box can hold, and a run takes the
// first free one together with a slot of the same global pool a cargo build
// draws from, so a race run and a cargo build never stack uncounted on top of
// each other (cold review on #421).
//
// The number of keys on offer is the smaller of
//
//	free memory / raceMemPerRunMB    and    cores / raceThreadsPerRun
//
// never below one. The two figures are what internal/run's governor already
// prices a heavy child at (threadsPerSlot 8, memoryPerSlotMB 8 GB): a race
// build is the heaviest child the gate starts, so it is held to the same
// price. That governor is not used here because it is an in-process
// semaphore, and the runs it would count are separate gate processes in
// separate lanes: only the file locks every process takes see all of them.
//
// Three rules keep the change in the safe direction:
//
//	unknown is one. A box whose free memory cannot be read gets one run at a
//	   time, exactly as before.
//	free memory is read NOW, and a running race build is itself eating it, so
//	   each run admitted lowers the number the next one is judged by.
//	the pool still bounds it. The count never exceeds APHROLLO_BUILD_SLOTS,
//	   the pool the cargo builds share, so a box tuned to one heavy job at a
//	   time stays at one.
const (
	raceThreadsPerRun = 8
	raceMemPerRunMB   = 8 * 1024
)

// raceCapacity is how many race runs a box with this many cores and this much
// free memory carries at once.
func raceCapacity(cores int, availMB int64) int {
	if cores <= 0 || availMB <= 0 {
		return 1
	}
	return max(1, min(int(availMB/raceMemPerRunMB), cores/raceThreadsPerRun))
}

// raceMachineFn reads the box the capacity is judged on: its hardware
// threads and the memory free right now, in megabytes. A seam for the tests.
var raceMachineFn = func() (cores int, availMB int64) {
	return runtime.NumCPU(), memBoxFn().AvailMB
}

// SetRaceMachineForTest pins the box the race capacity is judged on and
// answers the restore: no test machine is the box a capacity rule is about.
func SetRaceMachineForTest(cores int, availMB int64) (restore func()) {
	prev := raceMachineFn
	raceMachineFn = func() (int, int64) { return cores, availMB }
	return func() { raceMachineFn = prev }
}

// boxHeavyCapacity is how many heavy children (about 8 threads and 8 GB each)
// the box carries at once now: the race rule, without the slot pool's bound.
func boxHeavyCapacity() int {
	cores, availMB := raceMachineFn()
	return raceCapacity(cores, availMB)
}

// raceSlotCapacity is the number of race keys on offer right now: the box's
// capacity, held to the shared slot pool.
func raceSlotCapacity() int {
	cores, availMB := raceMachineFn()
	return min(raceCapacity(cores, availMB), buildSlotCount())
}

// raceKeyAt is the i-th race key. The first is the key every race run took
// before there were several, so a binary that predates them still excludes
// and is excluded on it.
func raceKeyAt(i int) string {
	if i == 0 {
		return goRaceLockKey()
	}
	return fmt.Sprintf("%s.%d", goRaceLockKey(), i)
}

// TryAcquireRaceSlot attempts, once and without blocking, to take a race key
// and a slot of the global pool. ok=false means the box is carrying as many
// race runs as it should, or its pool is full.
func TryAcquireRaceSlot(cmd, cwd string) (BuildSlot, func(), bool) {
	for i := range raceSlotCapacity() {
		if slot, release, ok := TryAcquireBuildSlot(raceKeyAt(i), cmd, cwd); ok {
			return slot, release, true
		}
	}
	return BuildSlot{}, func() {}, false
}

// raceQueueOut is where a waiting race run says where it stands, a seam for
// the tests.
var raceQueueOut = func(line string) { fmt.Fprintln(os.Stderr, line) }

// acquireRaceSlot polls for a race slot until one is held or deadline
// elapses. A waiter that is going to wait says so at once and then every
// buildLockQueueNoticeEvery: where it stands in the queue, how long that is
// expected to take and who it is waiting for, on ONE line. A ZERO deadline is
// a single try, silent: the edit hook's contract.
func acquireRaceSlot(deadline time.Duration, cmd, cwd string) (BuildSlot, func(), bool) {
	if slot, release, ok := TryAcquireRaceSlot(cmd, cwd); ok {
		return slot, release, true
	}
	if deadline <= 0 {
		return BuildSlot{}, func() {}, false
	}
	defer WriteQueueWaiter(goRaceLockKey(), cmd, cwd)()
	start := time.Now()
	var nextNotice time.Duration
	for {
		waited := time.Since(start)
		if waited >= nextNotice {
			raceQueueOut(raceQueueLine())
			nextNotice += buildLockQueueNoticeEvery
		}
		if slot, release, ok := TryAcquireRaceSlot(cmd, cwd); ok {
			return slot, release, true
		}
		if waited >= deadline {
			return BuildSlot{}, func() {}, false
		}
		time.Sleep(buildLockPollInterval)
	}
}

// raceQueueLine is the line a waiting race run prints now.
func raceQueueLine() string {
	pos, total := raceQueuePosition(SnapshotQueueWaiters(), os.Getpid())
	est := raceQueueEstimate(recordedRaceSecs(), pos, raceSlotCapacity())
	return formatRaceQueueLine(pos, total, est, raceHolderText())
}

// formatRaceQueueLine is the one line: `gate: merge queue position 2 of 4
// (est. ~11 min; holder: <lane> pid 4242)`. With no estimate (no recorded run
// to base one on) that part is left out rather than guessed.
func formatRaceQueueLine(pos, total int, est time.Duration, holder string) string {
	if est <= 0 {
		return fmt.Sprintf("gate: merge queue position %d of %d (holder: %s)", pos, total, holder)
	}
	mins := int((est + time.Minute - 1) / time.Minute)
	return fmt.Sprintf("gate: merge queue position %d of %d (est. ~%d min; holder: %s)", pos, total, mins, holder)
}

// raceQueuePosition is pid's place among the race waiters, oldest first, and
// how many are waiting. A waiter with no record (its write failed) is counted
// as the last.
func raceQueuePosition(waiters []QueueWaiter, pid int) (pos, total int) {
	var line []QueueWaiter
	for _, w := range waiters {
		if w.Target == goRaceLockKey() {
			line = append(line, w)
		}
	}
	sort.Slice(line, func(i, j int) bool {
		if !line[i].Started.Equal(line[j].Started) {
			return line[i].Started.Before(line[j].Started)
		}
		return line[i].PID < line[j].PID
	})
	for i, w := range line {
		if w.PID == pid {
			return i + 1, len(line)
		}
	}
	return len(line) + 1, len(line) + 1
}

// raceQueueEstimate is how long the waiter at pos should expect to wait: the
// median of recorded race run times for each round of runs ahead of it, a
// round being as many runs as the box carries at once. 0 when nothing is
// recorded.
func raceQueueEstimate(secs []float64, pos, capacity int) time.Duration {
	if len(secs) == 0 || pos < 1 {
		return 0
	}
	sorted := append([]float64(nil), secs...)
	sort.Float64s(sorted)
	median := sorted[nearestRankIndex(len(sorted), 0.5)]
	rounds := (pos + max(capacity, 1) - 1) / max(capacity, 1)
	return time.Duration(median*float64(rounds)) * time.Second
}

// recordedRaceSecs is how long the recent completed `go test -race` runs took,
// whichever stage ran them: the queue's estimate is about this kind of run,
// and a merge's runs are recorded under paths that never appear again.
func recordedRaceSecs() []float64 {
	return recordedSecsWhere(func(e gateEntry) bool {
		return strings.HasPrefix(e.Cmd, "go test") && strings.Contains(e.Cmd, " -race")
	}, suiteFloorWindow)
}

// raceHolderText names the longest-running race holder, "unknown" when no
// record of one is readable (the owner file is best-effort).
func raceHolderText() string {
	var oldest *BuildLockOwner
	for i := range buildSlotCount() {
		o, ok := ReadBuildSlotOwner(raceKeyAt(i))
		if !ok || !pidRunningFn(o.PID) {
			continue
		}
		if oldest == nil || o.Started.Before(oldest.Started) {
			oldest = &o
		}
	}
	if oldest == nil {
		return "unknown"
	}
	return fmt.Sprintf("%s pid %d", filepath.Base(oldest.Cwd), oldest.PID)
}
