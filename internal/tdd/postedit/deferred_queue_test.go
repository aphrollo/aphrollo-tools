package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// queueScene is a project with a run of ./a already going for the session: the
// state issue #1188 was filed from. The foreground budget is zero, so an edit's
// hook never waits for a run that is made not to finish.
func queueScene(t *testing.T) (root, session string, started *int) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	root = t.TempDir()
	started = liveSpawner(t)
	session = "s-1188"
	write(t, root, "a/a.go", "package a\n")
	if out := runEditPhases(coalesceRunner("./a"), root, filepath.Join(root, "a", "a.go"), "", sourceIdentity(root, filepath.Join(root, "a", "a.go")), session, "", 0); !out.deferred {
		t.Fatalf("setup: the first run must be left going, got %+v", out)
	}
	return root, session, started
}

func editOf(t *testing.T, root, session, pkg, body string) string {
	t.Helper()
	file := filepath.Join(root, pkg, pkg+".go")
	write(t, root, pkg+"/"+pkg+".go", body)
	line, _ := postEditDeferred(stateSnapshot{runner: coalesceRunner("./" + pkg)}, root, file, "", session)
	return tddtest.Pathless(t, line)
}

// An edit made while a run of another package is going gets its own line,
// naming the command it will run and that it has not run: not the running job's
// BUILDING line with no command (issue #1188).
func TestPostEditDeferred_AnEditBehindARunGetsItsOwnQueuedLine(t *testing.T) {
	root, session, started := queueScene(t)

	line := editOf(t, root, session, "b", "package b\n")

	for _, want := range []string{"go test ./b", "QUEUED", `"go test ./a" is still going`, "place 1 of 1", "not tested yet"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not carry %q", line, want)
		}
	}
	if strings.Contains(line, "BUILDING") || strings.Contains(line, "status --wait") {
		t.Errorf("line %q offers a wait on a run that is not the edit's", line)
	}
	if *started != 1 {
		t.Errorf("runs started = %d, want the running one only: the edit's waits for the slot", *started)
	}
}

// A newer edit of the same run replaces the waiting request, and a different
// run queues behind it: first in, first out, one entry per run.
func TestPostEditDeferred_ANewerEditOfTheSameRunReplacesTheWaitingOne(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	editOf(t, root, session, "c", "package c\n")

	line := editOf(t, root, session, "b", "package b // again\n")

	if !strings.Contains(line, "replacing an older request") || !strings.Contains(line, "place 2 of 2") {
		t.Fatalf("line %q must say it replaced the waiting b and now stands behind c", line)
	}
	q := readQueue(queuePath(session, root))
	if len(q.Runs) != 2 || q.Runs[0].Runner[1] != "test" || q.Runs[0].Runner[2] != "./c" || q.Runs[1].Runner[2] != "./b" {
		t.Fatalf("queue = %+v, want c then the newer b", q.Runs)
	}
}

// Past the bound the edit is told it was not tested, with the command it would
// have run, not left with a line that reads as in progress.
func TestPostEditDeferred_AFullQueueAnswersQueuedSkipped(t *testing.T) {
	root, session, _ := queueScene(t)
	for i := range maxQueuedRuns {
		editOf(t, root, session, "p"+string(rune('a'+i)), "package p\n")
	}

	line := editOf(t, root, session, "overflow", "package overflow\n")

	if !strings.Contains(line, "QUEUED-SKIPPED") || !strings.Contains(line, "go test ./overflow") || !strings.Contains(line, "NOT tested") {
		t.Fatalf("line %q must be QUEUED-SKIPPED, name the command, and say it was not tested", line)
	}
}

// The edit's own run, going for an older tree state, is told to restart on the
// newest source; the line says the verdict now coming is the older one and
// offers no foreground wait on it (issue #1189).
func TestPostEditDeferred_TheSameRunForAMovedTreeSaysItRestartsAndOffersNoWait(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, session, started := queueScene(t)
	write(t, root, "a/a.go", "package a // moved\n")

	line := editOf(t, root, session, "a", "package a // moved again\n")

	if !strings.Contains(line, "go test ./a") || !strings.Contains(line, "restarts on the newest source") || strings.Contains(line, "status --wait") {
		t.Fatalf("line %q must name the run, say it restarts, and offer no wait", line)
	}
	if *started != 1 {
		t.Errorf("runs started = %d, want 1: the restart waits for the running one", *started)
	}
}

// The slot frees when the running job is harvested, and the oldest waiting run
// starts then, on the tree as it stands. The edits that queued moved the tree
// under the running job, so that one is restarted first (the coverage it had is
// not lost); once the restart is harvested for the unmoved tree the queue goes.
func TestHarvestSessionJobs_StartsTheOldestWaitingRunWhenTheSlotFrees(t *testing.T) {
	root, session, started := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	editOf(t, root, session, "c", "package c\n")
	job, ok := loadDeferredJob(session, root)
	if !ok || !job.Dirty {
		t.Fatalf("setup: the running job must be marked dirty by the edits behind it, got %+v", job)
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	harvestSessionJobs(session)

	if *started != 2 {
		t.Fatalf("runs started = %d, want the first and its restart on the newest source", *started)
	}
	again, _ := loadDeferredJob(session, root)
	if !strings.HasSuffix(strings.Join(again.Runner, " "), " ./a") || again.Dirty {
		t.Fatalf("running job = %+v, want the restart of ./a, clean", again)
	}
	writePhaseResult(again.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	lines := harvestSessionJobs(session)

	if *started != 3 {
		t.Fatalf("runs started = %d, want the oldest waiting run started once the slot freed", *started)
	}
	next, ok := loadDeferredJob(session, root)
	if !ok || !strings.HasSuffix(strings.Join(next.Runner, " "), " ./b") {
		t.Fatalf("running job = %+v, want the oldest waiting run, ./b", next)
	}
	if q := readQueue(queuePath(session, root)); len(q.Runs) != 1 || q.Runs[0].Runner[2] != "./c" {
		t.Fatalf("queue = %+v, want only c still waiting", q.Runs)
	}
	if joined := tddtest.Pathless(t, strings.Join(lines, "\n")); !strings.Contains(joined, "go test") || !strings.Contains(joined, "./b") || !strings.Contains(joined, "the queued run started") {
		t.Fatalf("the sweep must print that the queued run started:\n%s", joined)
	}
}

// Two hooks of one session pump together: one run starts, and the other entry
// stays waiting, not lost to a second spawn into the same job record.
func TestPumpQueue_TwoHooksStartOneRunAndKeepTheRest(t *testing.T) {
	root, session, started := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	editOf(t, root, session, "c", "package c\n")
	job, _ := loadDeferredJob(session, root)
	clearDeferredJob(session, root)
	_ = job

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pumpQueue(session, root)
		}()
	}
	wg.Wait()

	if *started != 2 {
		t.Fatalf("runs started = %d, want the first and exactly one queued run", *started)
	}
	if q := readQueue(queuePath(session, root)); len(q.Runs) != 1 {
		t.Fatalf("queue = %+v, want the second entry still waiting", q.Runs)
	}
}

// A queued run that cannot start is the edit's own not-tested outcome: the sweep
// says so instead of logging it where nobody reads.
func TestHarvestSessionJobs_ReportsAQueuedRunThatCouldNotStart(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	clearDeferredJob(session, root)
	t.Cleanup(SetSpawnPhaseForTest(func(j DeferredJob) (DeferredJob, bool) { return j, false }))

	lines := harvestSessionJobs(session)

	joined := tddtest.Pathless(t, strings.Join(lines, "\n"))
	if !strings.Contains(joined, "go test ./b") || !strings.Contains(joined, "could not be started") || !strings.Contains(joined, "NOT tested") {
		t.Fatalf("the sweep must say the queued run could not start:\n%s", joined)
	}
	if q := readQueue(queuePath(session, root)); len(q.Runs) != 0 {
		t.Fatalf("queue = %+v, want the failed entry gone, not retried forever", q.Runs)
	}
}

// ratchet: test_removed TestPumpQueue_GivesUpOnAnEntryThatWaitedTooLong: an entry is no longer dropped for its age; it is dropped only when the run in front of it will never finish (TestPumpQueue_DropsTheEntriesBehindARunThatNeverFinished), and a long healthy chain keeps it waiting.

// A queue gives up only on a run that will never finish. Behind a healthy chain
// an entry waits as long as the chain takes, however long that is.
func TestPumpQueue_AnEntryBehindAHealthyRunIsNotDroppedHoweverLongItWaited(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	path := queuePath(session, root)
	q := readQueue(path)
	q.Runs[0].At = time.Now().Add(-3 * deferredMax())
	writeQueue(path, q)

	lines := pumpQueue(session, root)

	if len(lines) != 0 || len(readQueue(path).Runs) != 1 {
		t.Fatalf("lines %q, queue %+v: want the entry still waiting behind a run that is alive", lines, readQueue(path).Runs)
	}
}

// The run in front of the queue is gone: its process is dead and it left no
// result, so nothing will ever start the entries behind it. They are dropped,
// logged, and the line says why.
func TestPumpQueue_DropsTheEntriesBehindARunThatNeverFinished(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	t.Cleanup(SetProcessStartTimeForTest(func(int) (time.Time, bool) { return time.Time{}, false }))

	lines := pumpQueue(session, root)

	joined := tddtest.Pathless(t, strings.Join(lines, "\n"))
	if !strings.Contains(joined, "QUEUED-DROPPED") || !strings.Contains(joined, "never finished") || !strings.Contains(joined, "NOT tested") {
		t.Fatalf("lines = %q, want the dropped entry reported as not tested, with the true cause", joined)
	}
	if !strings.Contains(gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR")), "queued-dropped") {
		t.Error("the drop left no queued-dropped entry in the gate log")
	}
}

// The pump started a run between this hook's look at the slot and its own
// spawn: under the queue's lock the edit sees that run, and waits behind it
// instead of starting into the same job record.
func TestRunEditPhases_AFreshStartBehindARunTheSlotHoldsQueuesInstead(t *testing.T) {
	root, session, started := queueScene(t)
	file := filepath.Join(root, "b", "b.go")
	write(t, root, "b/b.go", "package b\n")

	out := runEditPhases(coalesceRunner("./b"), root, file, "", sourceIdentity(root, file), session, "", 0)

	if !out.deferred || out.logToken != "queue-waiting" || !strings.Contains(out.notice, "QUEUED") {
		t.Fatalf("outcome %+v, want the edit queued behind the run going", out)
	}
	if *started != 1 {
		t.Errorf("runs started = %d, want the running one only", *started)
	}
	if got := readQueue(queuePath(session, root)); len(got.Runs) != 1 || got.Runs[0].Runner[2] != "./b" {
		t.Errorf("queue = %+v, want ./b waiting", got.Runs)
	}
}

// An edit that is already going for a tree that moved: nothing was queued, so
// the log does not say it was; and a run known to be stale offers no wait.
func TestPostEditDeferred_TheSameRunForAMovedTreeLogsARestartNotAQueue(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, session, _ := queueScene(t)
	write(t, root, "a/a.go", "package a // moved\n")

	editOf(t, root, session, "a", "package a // moved again\n")

	log := gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR"))
	if strings.Contains(log, " queue-waiting ") || !strings.Contains(log, "deferred-restart") {
		t.Fatalf("gate log:\n%s\nwant deferred-restart and no queue-waiting for a run that queued nothing", log)
	}
	line := editOf(t, root, session, "a", "package a // moved a third time\n")
	if strings.Contains(line, "status --wait") {
		t.Fatalf("line %q offers a wait on a run known to be stale", line)
	}
}

// A queued edit is not a run: it must not inflate the denominators.
func TestPostEditDeferred_AQueuedEditLogsAQueueWaitingEntry(t *testing.T) {
	root, session, _ := queueScene(t)

	editOf(t, root, session, "b", "package b\n")

	if log := gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR")); !strings.Contains(log, "queue-waiting") {
		t.Fatalf("gate log:\n%s\nwant a queue-waiting entry for the edit that queued", log)
	}
}

// An edit that starts its own run for a request that is waiting supersedes it.
func TestPostEditDeferred_StartingARunDropsTheWaitingRequestForIt(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	job, _ := loadDeferredJob(session, root)
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})
	if err := os.Remove(job.Result); err != nil { // the harvest below must find no result to report, and a free slot
		t.Fatal(err)
	}
	clearDeferredJob(session, root)

	editOf(t, root, session, "b", "package b // now\n")

	if q := readQueue(queuePath(session, root)); len(q.Runs) != 0 {
		t.Fatalf("queue = %+v, want the waiting b dropped by the b run that started", q.Runs)
	}
}

// A session that ends takes its waiting runs with it: nothing would harvest
// what they started.
func TestReapSessionDeferredJobs_ForgetsTheSessionsWaitingRuns(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	if q := readQueue(queuePath(session, root)); len(q.Runs) != 1 {
		t.Fatalf("setup: queue = %+v, want one waiting run", q.Runs)
	}

	reapSessionDeferredJobs(session)

	if _, err := os.Stat(queuePath(session, root)); !os.IsNotExist(err) {
		t.Fatalf("the queue file of an ended session is still there (stat err %v)", err)
	}
	if !strings.Contains(gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR")), "queued-dropped") {
		t.Fatal("the dropped entry left no queued-dropped entry in the gate log: the edit's run just vanished")
	}
}

// A record whose process identity was never taken (the OS query failed at spawn)
// says nothing about its process. It is a healthy run until something shows
// otherwise: the queue behind it is kept, and an edit queues behind it instead of
// starting into its job record.
func TestPumpQueue_ARunWithNoRecordedProcessIdentityIsStillGoing(t *testing.T) {
	root, session, started := queueScene(t)
	updateDeferredJob(session, root, func(j *DeferredJob) { j.PIDCreatedAt = time.Time{} })
	t.Cleanup(SetProcessStartTimeForTest(func(int) (time.Time, bool) { return time.Time{}, false }))
	editOf(t, root, session, "b", "package b\n")

	lines := pumpQueue(session, root)

	if len(lines) != 0 || len(readQueue(queuePath(session, root)).Runs) != 1 {
		t.Fatalf("lines %q, queue %+v: want the entry kept behind a run that may well be alive", lines, readQueue(queuePath(session, root)).Runs)
	}
	file := filepath.Join(root, "c", "c.go")
	write(t, root, "c/c.go", "package c\n")
	out := runEditPhases(coalesceRunner("./c"), root, file, "", sourceIdentity(root, file), session, "", 0)
	if !out.deferred || out.logToken != "queue-waiting" || *started != 1 {
		t.Fatalf("outcome %+v, started %d: want the edit queued, nothing spawned over the live job", out, *started)
	}
}

// A run whose process is gone is not one to queue behind: the edit is not held
// by it and nothing waits for a run that will never finish.
func TestPostEditDeferred_ADeadRunDoesNotHoldTheEditsBehindIt(t *testing.T) {
	root, session, _ := queueScene(t)
	t.Cleanup(SetProcessStartTimeForTest(func(int) (time.Time, bool) { return time.Time{}, false }))
	file := filepath.Join(root, "b", "b.go")
	write(t, root, "b/b.go", "package b\n")

	line, held := activeRunLine(stateSnapshot{runner: coalesceRunner("./b")}, root, file, session, sourceIdentity(root, file))

	if held || line != "" {
		t.Fatalf("held=%v line %q: a dead run must not hold the edit", held, line)
	}
	if len(readQueue(queuePath(session, root)).Runs) != 0 {
		t.Fatal("an entry was queued behind a dead run")
	}
}

// A rung of the widening ladder that has to wait behind another run says so, and
// logs it as waiting, not as a deferred build with a wait on offer.
func TestWidenDeferredSelection_AQueuedRungSaysQueuedAndLogsIt(t *testing.T) {
	root, session, _ := queueScene(t)
	write(t, root, "go.mod", "module m\n\ngo 1.22\n")
	write(t, root, "gen/gen.go", "package gen\n")
	write(t, root, "user/user.go", "package user\n\nimport _ \"m/gen\"\n")
	write(t, root, "user/user_test.go", "package user\n\nimport \"testing\"\n\nfunc TestAUser_Ok(t *testing.T) {}\n")
	narrow := Runner{Cmd: "go", Args: []string{"test", "./gen"}}

	w := widenDeferredSelection(narrow, root, filepath.Join(root, "gen", "gen.go"), "", "h", session, "", time.Now().Add(time.Minute), SuiteResult{})

	line := tddtest.Pathless(t, w.terminal)
	if !strings.Contains(line, "QUEUED") || strings.Contains(line, "BUILDING") || strings.Contains(line, "status --wait") {
		t.Fatalf("line %q, want the rung queued behind the running run, and no wait offered", line)
	}
	if !strings.Contains(gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR")), "queue-waiting") {
		t.Error("the queued rung was not logged as waiting")
	}
}

// A rung that finds the queue full was not kept: QUEUED-SKIPPED, not BUILDING.
func TestWidenDeferredSelection_AQueueFullRungSaysSkipped(t *testing.T) {
	root, session, _ := queueScene(t)
	write(t, root, "go.mod", "module m\n\ngo 1.22\n")
	write(t, root, "gen/gen.go", "package gen\n")
	write(t, root, "user/user.go", "package user\n\nimport _ \"m/gen\"\n")
	write(t, root, "user/user_test.go", "package user\n\nimport \"testing\"\n\nfunc TestAUser_Ok(t *testing.T) {}\n")
	for i := range maxQueuedRuns {
		enqueueRun(session, root, queuedRun{Runner: []string{"go", "test", "./p" + string(rune('a'+i))}, Dir: root, At: time.Now()})
	}
	narrow := Runner{Cmd: "go", Args: []string{"test", "./gen"}}

	w := widenDeferredSelection(narrow, root, filepath.Join(root, "gen", "gen.go"), "", "h", session, "", time.Now().Add(time.Minute), SuiteResult{})

	if line := tddtest.Pathless(t, w.terminal); !strings.Contains(line, "QUEUED-SKIPPED") || strings.Contains(line, "BUILDING") {
		t.Fatalf("line %q, want QUEUED-SKIPPED", line)
	}
	if !strings.Contains(gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR")), "queued-skipped") {
		t.Error("the skipped rung was not logged as queued-skipped")
	}
}

// The line and the log of a run the harvest could not start because another run
// holds the slot: a full queue is QUEUED-SKIPPED and logged, never "place 0 of 0".
func TestQueueHeldLine_AFullQueueIsSkippedAndAWaitingOneIsLogged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := coalesceRunner("./b")
	blocker := DeferredJob{Phase: "run", Runner: []string{"go", "test", "./a"}}

	full := tddtest.Pathless(t, queueHeldLine(r, "/r", blocker, queueOutcome{full: true}))
	waiting := tddtest.Pathless(t, queueHeldLine(r, "/r", blocker, queueOutcome{position: 2, waiting: 3}))

	if !strings.Contains(full, "QUEUED-SKIPPED") || strings.Contains(full, "place") {
		t.Errorf("full: %q", full)
	}
	if !strings.Contains(waiting, "place 2 of 3") {
		t.Errorf("waiting: %q", waiting)
	}
	log := gateLogText(t, os.Getenv("CLAUDE_CONFIG_DIR"))
	if !strings.Contains(log, "queued-skipped") || !strings.Contains(log, "queue-waiting") {
		t.Errorf("gate log:\n%s\nwant both tokens", log)
	}
}

// The run an edit queues and the run a start finds the slot held for are one
// unit, however each spelled its command: the second request replaces the first.
func TestQueue_TheEditPathAndTheStartPathKeyTheSameRunTheSame(t *testing.T) {
	root, session, _ := queueScene(t)
	editOf(t, root, session, "b", "package b\n")
	file := filepath.Join(root, "b", "b.go")

	out := runEditPhases(coalesceRunner("./b"), root, file, "", sourceIdentity(root, file), session, "", 0)

	if !strings.Contains(out.notice, "replacing an older request") {
		t.Fatalf("notice %q, want the start path to have replaced the waiting request", out.notice)
	}
	if got := readQueue(queuePath(session, root)); len(got.Runs) != 1 {
		t.Fatalf("queue = %+v, want one entry for the one run", got.Runs)
	}
}

// A wait goes on past a line that says the run is still ahead of it or going; it
// stops at one that says the run will never start.
func TestIsHeldLine_QueuedAndBuildingAreNotFinal(t *testing.T) {
	cases := map[string]bool{
		"gate: → BUILDING (deferred; x)":                         true,
		"gate: go test ./b in /r → QUEUED (deferred; x)":         true,
		"gate: go test ./b in /r → QUEUED-SKIPPED (deferred; x)": false,
		"gate: go test ./b in /r → QUEUED-DROPPED (x)":           false,
		"gate: deferred go test ./b in /r → green (3 passed)":    false,
		"": false,
	}
	for line, want := range cases {
		if got := isHeldLine(line); got != want {
			t.Errorf("isHeldLine(%q) = %v, want %v", line, got, want)
		}
	}
}

// A job record that names no command (written before the field existed) cannot
// be told from the edit's own run: the edit is not queued behind "" but marks it
// dirty and gets the BUILDING line, and the line of the same run for a moved tree
// is BUILDING too, naming the command.
func TestActiveRunLine_ARecordWithNoCommandIsNotQueuedBehind(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Session: "s-old", Phase: "build", Dir: root, PID: 4242, Started: time.Now(), FileHash: "stale"})
	file := filepath.Join(root, "b", "b.go")

	line, held := activeRunLine(stateSnapshot{runner: coalesceRunner("./b")}, root, file, "s-old", "newer")

	if held || line != "" {
		t.Fatalf("held=%v line %q: a record with no command must fall to the harvest's own path", held, line)
	}
	if got := queuedSameRunLine(coalesceRunner("./b"), "/r"); !strings.Contains(got, "BUILDING") || !strings.Contains(got, "go test ./b") {
		t.Fatalf("same-run line %q, want BUILDING and the command", got)
	}
}
