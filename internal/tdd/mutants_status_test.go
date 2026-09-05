package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session with no lane commit at all, and nobody has ever run a mutation
// job or measurement for this tree: there is nothing to report but "never
// started".
func TestComputeMutantsStatus_NoneWhenNothingHasEverMeasuredThisTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")

	rep, err := ComputeMutantsStatus(root)
	if err != nil {
		t.Fatalf("ComputeMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunNone {
		t.Fatalf("State = %v, want MutantsRunNone", rep.State)
	}
}

// Outside a git repository there is no tree to name, and no lane to ask
// about — an error, not a silent "none".
func TestComputeMutantsStatus_ErrorsOutsideAGitRepository(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	if _, err := ComputeMutantsStatus(dir); err == nil {
		t.Fatal("ComputeMutantsStatus outside a git repository returned nil error, want one naming the problem")
	}
}

// A job is alive for this repo and nothing else holds the box-wide
// mutation-run lock: the run is actually measuring, not queued.
func TestComputeMutantsStatus_GoingReportsMeasuringWhenNoOtherProcessHoldsTheLock(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	defer SetLockDirForTest(t.TempDir())()
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	saveMutantsJob(MutantsJob{
		Repo: commonGitDir(root), Branch: "main", TipTree: tip,
		PID: os.Getpid(), Started: time.Now(),
	})

	rep, err := ComputeMutantsStatus(root)
	if err != nil {
		t.Fatalf("ComputeMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunGoing {
		t.Fatalf("State = %v, want MutantsRunGoing", rep.State)
	}
	if rep.WaitingOnLock {
		t.Fatal("WaitingOnLock = true, want false — nobody else holds the box-wide lock")
	}
	if rep.JobPID != os.Getpid() {
		t.Errorf("JobPID = %d, want %d", rep.JobPID, os.Getpid())
	}
}

// A DIFFERENT pid holds the box-wide mutation-run lock while this job's own
// process is alive: it is still queued behind that run, not measuring.
func TestComputeMutantsStatus_GoingReportsWaitingOnLockWhenAnotherPidHoldsIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	defer SetLockDirForTest(t.TempDir())()
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	saveMutantsJob(MutantsJob{
		Repo: commonGitDir(root), Branch: "main", TipTree: tip,
		PID: os.Getpid(), Started: time.Now(),
	})

	owner := BuildLockOwner{PID: os.Getpid() + 123456, Cwd: "D:/elsewhere", Cmd: "mutants run for someone-else", Started: time.Now()}
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutantsRunLockOwnerPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := ComputeMutantsStatus(root)
	if err != nil {
		t.Fatalf("ComputeMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunGoing {
		t.Fatalf("State = %v, want MutantsRunGoing", rep.State)
	}
	if !rep.WaitingOnLock {
		t.Fatal("WaitingOnLock = false, want true — a different pid holds the box-wide lock")
	}
	if !strings.Contains(rep.LockHolder, "someone-else") {
		t.Errorf("LockHolder = %q, want it to name the actual holder", rep.LockHolder)
	}
}

// A run that ended without a receipt is DIED, carrying the exit code and the
// log a session needs to diagnose it — the same facts the merge gate's own
// missingReceiptRemedy already reads out of loadMutantsDeath.
func TestComputeMutantsStatus_DiedReportsExitCodeAndLogPath(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	errLog := filepath.Join(t.TempDir(), "run.err")
	if err := os.WriteFile(errLog, []byte("boom: the producer crashed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := MutantsJob{Repo: commonGitDir(root), TipTree: tip, ErrLog: errLog}
	recordMutantsDeath(j, 17, mutantsDeathTail(j))

	rep, err := ComputeMutantsStatus(root)
	if err != nil {
		t.Fatalf("ComputeMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunDied {
		t.Fatalf("State = %v, want MutantsRunDied", rep.State)
	}
	if rep.DiedExit != 17 {
		t.Errorf("DiedExit = %d, want 17", rep.DiedExit)
	}
	if rep.DiedLog != errLog {
		t.Errorf("DiedLog = %q, want %q", rep.DiedLog, errLog)
	}
}

// A receipt on disk for this exact tree is the DONE state, carrying the
// verdict and every count a session used to grep out of the raw JSON by
// hand.
func TestComputeMutantsStatus_DoneReadsVerdictAndCounts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	r := MutationReceipt{
		TipTree: tip, Verdict: "pass", MutantsTotal: 10, Caught: 9, Accepted: 1,
		FinishedAt: time.Now(),
	}
	writeReceiptFile(MutationReceiptPathFor(tip), r)

	rep, err := ComputeMutantsStatus(root)
	if err != nil {
		t.Fatalf("ComputeMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunDone {
		t.Fatalf("State = %v, want MutantsRunDone", rep.State)
	}
	if rep.Receipt.Caught != 9 || rep.Receipt.MutantsTotal != 10 || rep.Receipt.Accepted != 1 {
		t.Errorf("Receipt = %+v, want the counts written to disk", rep.Receipt)
	}
}

// A receipt for the exact tree is read BEFORE the job registry is even
// consulted: once the proof exists, a lingering (or stale) running-job
// record must never mask it. This is the same precedence
// checkMutationReceipt already uses — the receipt lookup comes first, and
// blockMissingReceipt (which reads RunningMutantsJobs) is only the ELSE
// branch.
func TestComputeMutantsStatus_DoneTakesPrecedenceOverARunningJobRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	writeReceiptFile(MutationReceiptPathFor(tip), MutationReceipt{TipTree: tip, Verdict: "pass", FinishedAt: time.Now()})
	saveMutantsJob(MutantsJob{Repo: commonGitDir(root), Branch: "main", TipTree: tip, PID: os.Getpid(), Started: time.Now()})

	rep, err := ComputeMutantsStatus(root)
	if err != nil {
		t.Fatalf("ComputeMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunDone {
		t.Fatalf("State = %v, want MutantsRunDone even with a running-job record present", rep.State)
	}
}

// FormatMutantsStatus's exit code is what a script actually reads; the text
// is for the human. A clean pass merges, so it is the only receipt state
// that gets exit 0.
func TestFormatMutantsStatus_ExitPassWhenReceiptVerdictIsCleanPass(t *testing.T) {
	rep := MutantsStatusReport{Branch: "lane/x", TipTree: "abcd1234", State: MutantsRunDone,
		Receipt: MutationReceipt{Verdict: "pass"}}
	_, code := FormatMutantsStatus(rep)
	if code != ExitMutantsStatusPass {
		t.Fatalf("code = %d, want ExitMutantsStatusPass (%d)", code, ExitMutantsStatusPass)
	}
}

// An unaccepted survivor means the merge gate would refuse this exact
// receipt — status must say so distinctly from a clean pass, in the exit
// code a script reads.
func TestFormatMutantsStatus_ExitFailWhenReceiptHasUnacceptedSurvivors(t *testing.T) {
	rep := MutantsStatusReport{Branch: "lane/x", TipTree: "abcd1234", State: MutantsRunDone,
		Receipt: MutationReceipt{Verdict: "pass", Unaccepted: []MutantName{{Raw: "crates/a/src/lib.rs:1: replace + with -"}}}}
	text, code := FormatMutantsStatus(rep)
	if code != ExitMutantsStatusFail {
		t.Fatalf("code = %d, want ExitMutantsStatusFail (%d)", code, ExitMutantsStatusFail)
	}
	if !strings.Contains(text, "would not merge") {
		t.Errorf("text = %q, want it to say the receipt would not merge", text)
	}
}

func TestFormatMutantsStatus_ExitNoneWhenNoRunStarted(t *testing.T) {
	rep := MutantsStatusReport{Branch: "lane/x", TipTree: "abcd1234", State: MutantsRunNone}
	_, code := FormatMutantsStatus(rep)
	if code != ExitMutantsStatusNone {
		t.Fatalf("code = %d, want ExitMutantsStatusNone (%d)", code, ExitMutantsStatusNone)
	}
}

func TestFormatMutantsStatus_ExitGoingWhenAJobIsAlive(t *testing.T) {
	rep := MutantsStatusReport{Branch: "lane/x", TipTree: "abcd1234", State: MutantsRunGoing, JobPID: 4242, JobStarted: time.Now()}
	_, code := FormatMutantsStatus(rep)
	if code != ExitMutantsStatusGoing {
		t.Fatalf("code = %d, want ExitMutantsStatusGoing (%d)", code, ExitMutantsStatusGoing)
	}
}

func TestFormatMutantsStatus_ExitDiedWhenTheRunEndedWithoutAReceipt(t *testing.T) {
	rep := MutantsStatusReport{Branch: "lane/x", TipTree: "abcd1234", State: MutantsRunDied, DiedExit: 1, DiedAt: time.Now(), DiedLog: "run.err"}
	_, code := FormatMutantsStatus(rep)
	if code != ExitMutantsStatusDied {
		t.Fatalf("code = %d, want ExitMutantsStatusDied (%d)", code, ExitMutantsStatusDied)
	}
}

// --wait must not touch the process-wait primitive at all when the tree is
// already at a terminal state — a session watching a tip that already has a
// verdict must get its answer at once.
func TestWaitMutantsStatus_ReturnsImmediatelyWithoutWaitingWhenAlreadyTerminal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")

	called := false
	prev := waitForPIDExitFn
	waitForPIDExitFn = func(pid int) { called = true }
	t.Cleanup(func() { waitForPIDExitFn = prev })

	rep, err := WaitMutantsStatus(root)
	if err != nil {
		t.Fatalf("WaitMutantsStatus: %v", err)
	}
	if rep.State != MutantsRunNone {
		t.Fatalf("State = %v, want MutantsRunNone", rep.State)
	}
	if called {
		t.Fatal("waitForPIDExitFn was called for an already-terminal state")
	}
}

// --wait for a going run blocks on THAT job's own pid, then re-reads the
// state once the wait returns — proving the block happens on the process,
// never on a timed re-read of the receipt file.
func TestWaitMutantsStatus_BlocksOnTheJobThenRereadsTheTerminalReceipt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	defer SetLockDirForTest(t.TempDir())()
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	saveMutantsJob(MutantsJob{Repo: commonGitDir(root), Branch: "main", TipTree: tip, PID: os.Getpid(), Started: time.Now()})

	var waitedFor int
	prev := waitForPIDExitFn
	waitForPIDExitFn = func(pid int) {
		waitedFor = pid
		// Stand in for the job finishing while the caller was blocked.
		writeReceiptFile(MutationReceiptPathFor(tip), MutationReceipt{TipTree: tip, Verdict: "pass", MutantsTotal: 3, Caught: 3, FinishedAt: time.Now()})
	}
	t.Cleanup(func() { waitForPIDExitFn = prev })

	rep, err := WaitMutantsStatus(root)
	if err != nil {
		t.Fatalf("WaitMutantsStatus: %v", err)
	}
	if waitedFor != os.Getpid() {
		t.Fatalf("waited for pid %d, want the running job's own pid %d", waitedFor, os.Getpid())
	}
	if rep.State != MutantsRunDone {
		t.Fatalf("State after waiting = %v, want MutantsRunDone once the receipt appears", rep.State)
	}
}
