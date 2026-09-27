package tdd

import (
	"os"
	"strings"
	"testing"
)

// A merge queue killed with the session that started it (a box restart) left
// green PRs unmerged for hours with nobody told. Its record outlives it, and
// the next session in that repo reports it with the command that resumes it.

// goneQueuePID is a pid no process can hold: above every platform's pid ceiling.
const goneQueuePID = 1<<31 - 2

const stoppedQueueLine = "merge queue for #21 #22 #23 stopped at #22 (process gone) — resume: aphrollo workspace merge --wait --resume"

// queueIn is a queue in repo that merged #21 and left #22 and #23.
func queueIn(repo string) *MergeQueueRecord {
	return &MergeQueueRecord{Repo: repo, PRs: []MergeQueuePR{
		{PR: 21, Status: MergeQueueMerged},
		{PR: 22, Status: MergeQueuePending},
		{PR: 23, Status: MergeQueuePending},
	}}
}

// saveQueue records queueIn(repo) as held by pid, with no process identity.
func saveQueue(t *testing.T, repo string, pid int) {
	t.Helper()
	rec := queueIn(repo)
	rec.PID = pid
	saveRecord(t, rec)
}

// saveRecord writes rec as its repo's queue record.
func saveRecord(t *testing.T, rec *MergeQueueRecord) {
	t.Helper()
	if err := SaveMergeQueueRecord(rec); err != nil {
		t.Fatal(err)
	}
}

// liveQueueIn is queueIn(repo) held by this very process.
func liveQueueIn(repo string) *MergeQueueRecord {
	rec := queueIn(repo)
	rec.StampThisProcess()
	return rec
}

func TestHandleSessionStart_ReportsAMergeQueueWhoseProcessIsGone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	saveQueue(t, primary, goneQueuePID)

	for _, cwd := range []string{primary, lane} {
		got := HandleSessionStart([]byte(`{"session_id":"sess-queue","cwd":` + jsonString(cwd) + `}`))
		if !strings.Contains(got, stoppedQueueLine) {
			t.Errorf("session start in %s does not report the stopped queue; want %q in:\n%s", cwd, stoppedQueueLine, got)
		}
	}
}

func TestHandleSessionStart_IsSilentAboutAMergeQueueStillRunning(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	saveRecord(t, liveQueueIn(repo))

	got := HandleSessionStart([]byte(`{"session_id":"sess-live-queue","cwd":` + jsonString(repo) + `}`))
	if strings.Contains(got, "merge queue") {
		t.Errorf("session start reports a queue whose process is alive:\n%s", got)
	}
}

// After a reboot the queue's old pid can belong to an unrelated process: a
// live pid whose start does not match the one recorded is not the queue.
func TestHandleSessionStart_ReportsAQueueWhosePidNowBelongsToAnotherProcess(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	rec := liveQueueIn(repo)
	rec.Identity = "another-boot:1"
	saveRecord(t, rec)

	got := HandleSessionStart([]byte(`{"session_id":"sess-reused-pid","cwd":` + jsonString(repo) + `}`))
	if !strings.Contains(got, stoppedQueueLine) {
		t.Errorf("session start does not report a queue whose pid was reused; want %q in:\n%s", stoppedQueueLine, got)
	}
}

// A record that carries no process identity cannot prove its queue runs, so
// it reads as stopped even while its pid is alive.
func TestHandleSessionStart_ReportsAQueueRecordWithNoProcessIdentity(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	saveQueue(t, repo, os.Getpid())

	got := HandleSessionStart([]byte(`{"session_id":"sess-no-identity","cwd":` + jsonString(repo) + `}`))
	if !strings.Contains(got, stoppedQueueLine) {
		t.Errorf("session start does not report a record with no process identity; want %q in:\n%s", stoppedQueueLine, got)
	}
}

// A pid whose identity cannot be read is never live, even for a record that
// carries no identity of its own to compare.
func TestMergeQueueRecord_UnreadableIdentityIsNeverLive(t *testing.T) {
	t.Cleanup(SetPidRunningForTest(func(int) bool { return true }))
	rec := queueIn(t.TempDir())
	rec.PID = goneQueuePID
	if rec.Live() {
		t.Error("a record whose pid has no readable identity read as live")
	}
}

// The identity alone is not liveness: the pid must also still run.
func TestMergeQueueRecord_LiveNeedsItsPidRunning(t *testing.T) {
	rec := liveQueueIn(t.TempDir())
	if !rec.Live() {
		t.Fatal("a record stamped by this process must read as live")
	}
	t.Cleanup(SetPidRunningForTest(func(int) bool { return false }))
	if rec.Live() {
		t.Error("a record whose pid is not running read as live")
	}
}

func TestMergeQueueStoppedLine_NamesTheFirstPendingPR(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	saveQueue(t, repo, goneQueuePID)
	if got := MergeQueueStoppedLine(repo); got != stoppedQueueLine {
		t.Errorf("MergeQueueStoppedLine = %q, want %q", got, stoppedQueueLine)
	}
	if got := MergeQueueStoppedLine(t.TempDir()); got != "" {
		t.Errorf("MergeQueueStoppedLine with no record = %q, want \"\"", got)
	}
}
