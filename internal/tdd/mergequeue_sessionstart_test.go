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

// saveQueue records a queue in repo that merged #21 and left #22 and #23.
func saveQueue(t *testing.T, repo string, pid int) {
	t.Helper()
	rec := &MergeQueueRecord{Repo: repo, PID: pid, PRs: []MergeQueuePR{
		{PR: 21, Status: MergeQueueMerged},
		{PR: 22, Status: MergeQueuePending},
		{PR: 23, Status: MergeQueuePending},
	}}
	if err := SaveMergeQueueRecord(rec); err != nil {
		t.Fatal(err)
	}
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
	saveQueue(t, repo, os.Getpid())

	got := HandleSessionStart([]byte(`{"session_id":"sess-live-queue","cwd":` + jsonString(repo) + `}`))
	if strings.Contains(got, "merge queue") {
		t.Errorf("session start reports a queue whose process is alive:\n%s", got)
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
