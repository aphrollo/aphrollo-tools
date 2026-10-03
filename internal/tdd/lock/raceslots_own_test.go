package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What one `-race` run costs the box, in the units raceCapacity divides by:
// 8 hardware threads and 8 GB of free memory. Spelled as literals here so a
// change to either estimate is a deliberate edit of this table too.
func TestRaceCapacity_IsTheSmallerOfFreeMemoryAndCores(t *testing.T) {
	const gb = 1024
	for _, c := range []struct {
		name    string
		cores   int
		availMB int64
		want    int
	}{
		{"big box, cores bind at 3", 24, 64 * gb, 3},
		{"free memory binds: 23.6 GB free on 24 cores", 24, 23*gb + 600, 2},
		{"cores bind: 64 GB free on 4 cores", 4, 64 * gb, 1},
		{"one run's worth of memory and plenty of cores", 64, 8 * gb, 1},
		{"just under one run's memory still runs one", 64, 7 * gb, 1},
		{"no free memory at all still runs one", 64, 0, 1},
		{"16 GB free, 16 cores admit two", 16, 16 * gb, 2},
		{"16 GB free, 8 cores admit one", 8, 16 * gb, 1},
		{"a huge box is not capped by the formula", 128, 256 * gb, 16},
		{"unknown cores still runs one", 0, 64 * gb, 1},
	} {
		if got := raceCapacity(c.cores, c.availMB); got != c.want {
			t.Errorf("%s: raceCapacity(%d cores, %d MB free) = %d, want %d", c.name, c.cores, c.availMB, got, c.want)
		}
	}
}

// Two -race runs go side by side on a big box and queue one at a time on a
// small one, the box being what a test states and never what it runs on.
func TestTryAcquireRaceSlot_TwoAdmittedOnABigBoxOneOnASmallBox(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "4")

	t.Run("big box", func(t *testing.T) {
		defer SetRaceMachineForTest(32, 128*1024)()
		_, releaseA, okA := TryAcquireRaceSlot("go test -race ./a", t.TempDir())
		if !okA {
			t.Fatal("the first race run was refused on a big box")
		}
		defer releaseA()
		_, releaseB, okB := TryAcquireRaceSlot("go test -race ./b", t.TempDir())
		if !okB {
			t.Fatal("the second race run was refused on a 32-core, 128 GB box with nothing else running")
		}
		releaseB()
	})
	t.Run("small box", func(t *testing.T) {
		defer SetRaceMachineForTest(4, 6*1024)()
		_, releaseA, okA := TryAcquireRaceSlot("go test -race ./a", t.TempDir())
		if !okA {
			t.Fatal("the first race run was refused on a small box")
		}
		defer releaseA()
		if _, releaseB, okB := TryAcquireRaceSlot("go test -race ./b", t.TempDir()); okB {
			releaseB()
			t.Fatal("a second race run was admitted on a 4-core, 6 GB box: it must queue behind the first")
		}
	})
}

// A race run draws from the pool cargo builds draw from, so it never stacks
// uncounted on top of one (the point of #421's review): with the pool at 2,
// one cargo build and one race run fill it, however big the box is.
func TestTryAcquireRaceSlot_NeverExceedsTheSharedSlotPool(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	defer SetRaceMachineForTest(64, 512*1024)()

	_, releaseCargo, ok := TryAcquireBuildSlot(t.TempDir(), "cargo build", t.TempDir())
	if !ok {
		t.Fatal("setup: the cargo build must take a slot")
	}
	defer releaseCargo()
	_, releaseRace, ok := TryAcquireRaceSlot("go test -race ./a", t.TempDir())
	if !ok {
		t.Fatal("a race run was refused although one pool slot was free")
	}
	defer releaseRace()

	if _, release, ok := TryAcquireRaceSlot("go test -race ./b", t.TempDir()); ok {
		release()
		t.Fatal("a third heavy run was admitted into a pool of 2: race runs and cargo builds stacked uncounted")
	}
}

// The first race key is the key every older binary on the box still takes, so
// a mixed fleet keeps excluding each other on it.
func TestRaceKeyAt_FirstKeyIsTheOriginalGoRaceKey(t *testing.T) {
	withIsolatedBuildLock(t)
	if got, want := raceKeyAt(0), goRaceLockKey(); got != want {
		t.Fatalf("raceKeyAt(0) = %q, want the original %q", got, want)
	}
	if raceKeyAt(1) == raceKeyAt(0) || raceKeyAt(2) == raceKeyAt(1) {
		t.Fatalf("race keys repeat: %q %q %q", raceKeyAt(0), raceKeyAt(1), raceKeyAt(2))
	}
}

func TestFormatRaceQueueLine_NamesPositionEstimateAndHolder(t *testing.T) {
	for _, c := range []struct {
		name  string
		pos   int
		total int
		est   time.Duration
		hold  string
		want  string
	}{
		{"with an estimate", 2, 4, 11 * time.Minute, "race-capacity pid 4242",
			"gate: merge queue position 2 of 4 (est. ~11 min; holder: race-capacity pid 4242)"},
		{"no record to estimate from", 1, 1, 0, "unknown",
			"gate: merge queue position 1 of 1 (holder: unknown)"},
		{"an estimate under a minute reads as one", 1, 2, 20 * time.Second, "a pid 1",
			"gate: merge queue position 1 of 2 (est. ~1 min; holder: a pid 1)"},
	} {
		got := formatRaceQueueLine(c.pos, c.total, c.est, c.hold)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if strings.Contains(got, "\n") {
			t.Errorf("%s: the line spans more than one line: %q", c.name, got)
		}
	}
}

func TestRaceQueuePosition_OrdersTheRaceWaitersByArrival(t *testing.T) {
	withIsolatedBuildLock(t)
	t0 := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	waiters := []QueueWaiter{
		{PID: 30, Target: goRaceLockKey(), Started: t0.Add(2 * time.Minute)},
		{PID: 10, Target: goRaceLockKey(), Started: t0},
		{PID: 20, Target: goRaceLockKey(), Started: t0.Add(time.Minute)},
		{PID: 99, Target: "/some/cargo/target", Started: t0.Add(-time.Hour)},
	}
	for pid, want := range map[int]int{10: 1, 20: 2, 30: 3} {
		pos, total := raceQueuePosition(waiters, pid)
		if pos != want || total != 3 {
			t.Errorf("pid %d: position %d of %d, want %d of 3 (the cargo waiter is not in the race queue)", pid, pos, total, want)
		}
	}
	if pos, total := raceQueuePosition(waiters, 77); pos != 4 || total != 4 {
		t.Errorf("a waiter with no record: position %d of %d, want 4 of 4", pos, total)
	}
}

func TestRaceQueueEstimate_IsTheMedianRunTimesTheRoundsAhead(t *testing.T) {
	secs := []float64{300, 900, 600}
	for _, c := range []struct {
		pos, capacity int
		want          time.Duration
	}{
		{1, 2, 10 * time.Minute},
		{2, 2, 10 * time.Minute},
		{3, 2, 20 * time.Minute},
		{4, 1, 40 * time.Minute},
	} {
		if got := raceQueueEstimate(secs, c.pos, c.capacity); got != c.want {
			t.Errorf("position %d, capacity %d: estimate %s, want %s", c.pos, c.capacity, got, c.want)
		}
	}
	if got := raceQueueEstimate(nil, 3, 2); got != 0 {
		t.Errorf("with no recorded run the estimate is %s, want none (0)", got)
	}
}

// A waiting race run says where it stands once at once, then at most once per
// notice interval, one line each.
func TestAcquireRaceSlot_AnnouncesItsPlaceOncePerInterval(t *testing.T) {
	withIsolatedBuildLock(t)
	defer SetRaceMachineForTest(4, 4*1024)()
	_, hold, ok := TryAcquireRaceSlot("go test -race ./holder", t.TempDir())
	if !ok {
		t.Fatal("setup: the holder must get the one slot")
	}
	defer hold()

	var lines []string
	prevOut := raceQueueOut
	raceQueueOut = func(l string) { lines = append(lines, l) }
	defer func() { raceQueueOut = prevOut }()

	prevEvery := buildLockQueueNoticeEvery
	defer func() { buildLockQueueNoticeEvery = prevEvery }()

	buildLockQueueNoticeEvery = time.Hour
	if _, _, got := acquireRaceSlot(150*time.Millisecond, "go test -race ./waiter", t.TempDir()); got {
		t.Fatal("the waiter got a slot the holder still has")
	}
	if len(lines) != 1 {
		t.Fatalf("with a one-hour interval the waiter said %d lines, want exactly the first: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "gate: merge queue position 1 of 1 (") || !strings.Contains(lines[0], "holder: ") {
		t.Fatalf("line = %q, want the merge-queue shape naming a holder", lines[0])
	}

	lines = nil
	buildLockQueueNoticeEvery = 40 * time.Millisecond
	acquireRaceSlot(300*time.Millisecond, "go test -race ./waiter", t.TempDir())
	if len(lines) < 2 {
		t.Fatalf("with a 40ms interval over 300ms the waiter said %d lines, want it to repeat: %q", len(lines), lines)
	}
}

// With a zero deadline (the edit hook's single try) nothing is announced: the
// caller is not going to wait.
func TestAcquireRaceSlot_ASingleTryIsSilent(t *testing.T) {
	withIsolatedBuildLock(t)
	defer SetRaceMachineForTest(4, 4*1024)()
	_, hold, _ := TryAcquireRaceSlot("go test -race ./holder", t.TempDir())
	defer hold()
	var lines []string
	prev := raceQueueOut
	raceQueueOut = func(l string) { lines = append(lines, l) }
	defer func() { raceQueueOut = prev }()

	acquireRaceSlot(0, "go test -race ./waiter", t.TempDir())
	if len(lines) != 0 {
		t.Fatalf("a single try announced %q", lines)
	}
}

// The line names whoever holds a race key by the directory it runs in and its
// pid, and says unknown when nothing is held.
func TestRaceHolderText_NamesTheHolderByDirectoryAndPid(t *testing.T) {
	withIsolatedBuildLock(t)
	defer SetRaceMachineForTest(4, 4*1024)()
	if got := raceHolderText(); got != "unknown" {
		t.Fatalf("with nothing held raceHolderText() = %q, want unknown", got)
	}
	lane := filepath.Join(t.TempDir(), "lane-x")
	_, release, ok := TryAcquireRaceSlot("go test -race ./a", lane)
	if !ok {
		t.Fatal("setup: the holder must get the slot")
	}
	defer release()
	if got, want := raceHolderText(), fmt.Sprintf("lane-x pid %d", os.Getpid()); got != want {
		t.Fatalf("raceHolderText() = %q, want %q", got, want)
	}
}

// A waiter's record leaves with its wait, or the next waiter would count a
// ghost ahead of it.
func TestAcquireRaceSlot_LeavesNoWaiterRecordBehind(t *testing.T) {
	withIsolatedBuildLock(t)
	defer SetRaceMachineForTest(4, 4*1024)()
	_, hold, _ := TryAcquireRaceSlot("go test -race ./holder", t.TempDir())
	defer hold()
	prev := raceQueueOut
	raceQueueOut = func(string) {}
	defer func() { raceQueueOut = prev }()

	acquireRaceSlot(60*time.Millisecond, "go test -race ./waiter", t.TempDir())
	if left := SnapshotQueueWaiters(); len(left) != 0 {
		t.Fatalf("a finished wait left %d waiter record(s): %+v", len(left), left)
	}
}
