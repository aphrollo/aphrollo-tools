package tdd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #431: two lanes of one repo can each have a live job at once. The
// missing-receipt line must name the job measuring THIS tree, never
// "whichever job is newest in the registry" — the box-wide lock's holder is
// routinely a DIFFERENT lane's run, started later than the one this tree is
// actually waiting on.
func TestMutationReceipt_MissingReceiptNamesTheJobForThisTreeNotTheNewestOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	defer SetLockDirForTest(t.TempDir())()
	prevRunning := pidRunningFn
	pidRunningFn = func(pid int) bool { return pid == 20052 || pid == 26460 }
	t.Cleanup(func() { pidRunningFn = prevRunning })
	const otherTip = "2222222222222222222222222222222222222222"
	ownStarted := time.Now().Add(-38 * time.Minute)
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/interpolation-crate", TipTree: laneTip, PID: 20052, Started: ownStarted})
	// Saved SECOND, so it is newest in the registry -- and for a different
	// tree entirely. The old "take the last one" pick would name this job.
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/identity-crate", TipTree: otherTip, PID: 26460, Started: time.Now()})

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a running job is not a receipt: the merge still waits for one")
	}
	if !strings.Contains(got.Message, "lane/interpolation-crate") || !strings.Contains(got.Message, "20052") {
		t.Fatalf("message = %q, want it to name lane/interpolation-crate's own job (pid 20052), not the other lane's", got.Message)
	}
	if strings.Contains(got.Message, "26460") {
		t.Fatalf("message = %q, must not name pid 26460 -- that job measures a different tree", got.Message)
	}
}

// Issue #431: when this tree's OWN job is not the one holding the box-wide
// mutation-run lock, the line must say so plainly rather than read as "your
// receipt is minutes away" for a run that is actually queued behind another.
func TestMutationReceipt_MissingReceiptReportsQueuedBehindADifferentHolder(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	defer SetLockDirForTest(t.TempDir())()
	prevRunning := pidRunningFn
	pidRunningFn = func(pid int) bool { return pid == 20052 }
	t.Cleanup(func() { pidRunningFn = prevRunning })
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/interpolation-crate", TipTree: laneTip, PID: 20052, Started: time.Now().Add(-38 * time.Minute)})

	owner := BuildLockOwner{PID: 26460, Cwd: "D:/lane-identity-crate", Cmd: "mutants run for lane/identity-crate", Started: time.Now().Add(-19 * time.Minute)}
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutantsRunLockOwnerPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a running job is not a receipt: the merge still waits for one")
	}
	if !strings.Contains(got.Message, "queued behind") {
		t.Fatalf("message = %q, want it to say the tree's own job is queued behind the lock's actual holder", got.Message)
	}
	if !strings.Contains(got.Message, "lane/identity-crate") {
		t.Fatalf("message = %q, want it to name the actual lock holder", got.Message)
	}
}
