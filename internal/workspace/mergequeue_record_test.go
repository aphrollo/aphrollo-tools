package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A merge queue is a child of the session that started it, so a box restart
// kills it with no word to anyone. The queue therefore keeps a record in the
// gate's state dir while it runs: a later session reports a record whose
// process is gone, and `--resume` finishes the PRs it left.

// deadPID is a pid no process can hold: above every platform's pid ceiling.
const deadPID = 1<<31 - 2

// queueStatuses renders repo's record as "#pr=status ...", or "<none>".
func queueStatuses(t *testing.T, repo string) string {
	t.Helper()
	rec, err := tdd.LoadMergeQueueRecord(repo)
	if err != nil {
		t.Fatalf("LoadMergeQueueRecord: %v", err)
	}
	if rec == nil {
		return "<none>"
	}
	var parts []string
	for _, p := range rec.PRs {
		parts = append(parts, "#"+strconv.Itoa(p.PR)+"="+p.Status)
	}
	return strings.Join(parts, " ")
}

func TestMergeQueue_RecordIsWrittenUpdatedAsEachPRResolvesAndRemovedAtTheEnd(t *testing.T) {
	f := queueFake()
	f.lanes = f.lanes[1:] // lane/one has no worktree: refused by name
	seen := map[string]string{}
	f.onMerge = func(branch string) { seen[branch] = queueStatuses(t, "/r") }
	install(t, f)

	items, err := PlanMergeQueue("/r", []int{21, 22, 23})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	_ = RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb)

	if got, want := seen["lane/two"], "#21=refused #22=pending #23=pending"; got != want {
		t.Errorf("record while #22 merged = %q, want %q", got, want)
	}
	if got, want := seen["lane/three"], "#21=refused #22=merged #23=pending"; got != want {
		t.Errorf("record while #23 merged = %q, want %q", got, want)
	}
	if got := queueStatuses(t, "/r"); got != "<none>" {
		t.Errorf("record after every PR resolved = %q, want it removed", got)
	}
}

func TestMergeQueue_RecordNamesThisProcessTheRepoAndTheStart(t *testing.T) {
	f := queueFake()
	var rec *tdd.MergeQueueRecord
	f.onMerge = func(branch string) {
		if rec == nil {
			rec, _ = tdd.LoadMergeQueueRecord("/r")
		}
	}
	install(t, f)
	start := f.now

	items, _ := PlanMergeQueue("/r", []int{21})
	var out, errb bytes.Buffer
	if err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("RunMergeQueue: %v\n%s", err, out.String())
	}
	if rec == nil {
		t.Fatal("no queue record while the queue ran")
	}
	if rec.PID != os.Getpid() || rec.Repo != "/r" || !rec.Started.Equal(start) {
		t.Errorf("record = pid %d repo %q started %v, want pid %d repo /r started %v", rec.PID, rec.Repo, rec.Started, os.Getpid(), start)
	}
	if !rec.Live() {
		t.Errorf("the running queue's own record (identity %q) does not read as live", rec.Identity)
	}
}

// A stop leaves the PRs it never reached pending, so the record outlives the
// process and the next session can resume them.
func TestMergeQueue_StopKeepsTheRecordWithTheRestPending(t *testing.T) {
	f := queueFake()
	f.prs[0].steps = []ciStep{{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "failure")}}}
	install(t, f)

	items, _ := PlanMergeQueue("/r", []int{21, 22})
	var out, errb bytes.Buffer
	if err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb); err == nil {
		t.Fatal("a failed check must fail the queue")
	}
	if got, want := queueStatuses(t, "/r"), "#21=refused #22=pending"; got != want {
		t.Errorf("record after the stop = %q, want %q", got, want)
	}
}

func TestMergeQueue_LiveQueueRecordRefusesASecondQueue(t *testing.T) {
	f := queueFake()
	install(t, f)
	held := &tdd.MergeQueueRecord{Repo: "/r", PRs: []tdd.MergeQueuePR{{PR: 9, Status: tdd.MergeQueuePending}}}
	held.StampThisProcess()
	if err := tdd.SaveMergeQueueRecord(held); err != nil {
		t.Fatal(err)
	}

	items, _ := PlanMergeQueue("/r", []int{21})
	var out, errb bytes.Buffer
	err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("a second queue ran while a live one holds the repo")
	}
	if len(f.merged) != 0 {
		t.Errorf("merged %v past a live queue", f.merged)
	}
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("the refusal does not name the live queue's pid: %v", err)
	}
	if got := queueStatuses(t, "/r"); got != "#9=pending" {
		t.Errorf("the live queue's record = %q, want it untouched", got)
	}
}

// A queue whose process is gone does not block a new one.
func TestMergeQueue_DeadQueueRecordDoesNotBlockANewQueue(t *testing.T) {
	f := queueFake()
	install(t, f)
	gone := &tdd.MergeQueueRecord{Repo: "/r", PID: deadPID, PRs: []tdd.MergeQueuePR{{PR: 9, Status: tdd.MergeQueuePending}}}
	if err := tdd.SaveMergeQueueRecord(gone); err != nil {
		t.Fatal(err)
	}

	items, _ := PlanMergeQueue("/r", []int{21})
	var out, errb bytes.Buffer
	if err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("RunMergeQueue: %v\n%s", err, out.String())
	}
	if got := strings.Join(f.merged, ","); got != "lane/one" {
		t.Errorf("merged %q, want lane/one", got)
	}
}

func TestResumeMergeQueue_MergesOnlyThePRsTheStoppedQueueLeft(t *testing.T) {
	f := queueFake()
	seen := ""
	f.onMerge = func(branch string) {
		if branch == "lane/three" {
			seen = queueStatuses(t, "/r")
		}
	}
	install(t, f)
	gone := &tdd.MergeQueueRecord{Repo: "/r", PID: deadPID, PRs: []tdd.MergeQueuePR{
		{PR: 21, Status: tdd.MergeQueueMerged},
		{PR: 22, Status: tdd.MergeQueuePending},
		{PR: 23, Status: tdd.MergeQueuePending},
	}}
	if err := tdd.SaveMergeQueueRecord(gone); err != nil {
		t.Fatal(err)
	}

	prior, items, err := PlanResume("/r")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	var out, errb bytes.Buffer
	if err := ResumeMergeQueue("/r", prior, items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("ResumeMergeQueue: %v\n%s", err, out.String())
	}
	if got := strings.Join(f.merged, ","); got != "lane/two,lane/three" {
		t.Errorf("merged %q, want lane/two,lane/three — only the PRs left pending", got)
	}
	if want := "#21=merged #22=merged #23=pending"; seen != want {
		t.Errorf("resumed record while #23 merged = %q, want %q", seen, want)
	}
	if got := queueStatuses(t, "/r"); got != "<none>" {
		t.Errorf("record after the resumed queue finished = %q, want it removed", got)
	}
}

func TestPlanResume_RefusesWhileTheQueueIsStillLive(t *testing.T) {
	f := queueFake()
	install(t, f)
	live := &tdd.MergeQueueRecord{Repo: "/r", PRs: []tdd.MergeQueuePR{{PR: 22, Status: tdd.MergeQueuePending}}}
	live.StampThisProcess()
	if err := tdd.SaveMergeQueueRecord(live); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PlanResume("/r"); err == nil || !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("PlanResume on a live queue = %v, want a refusal naming its pid", err)
	}
}

func TestPlanResume_WithNoRecordSaysThereIsNothingToResume(t *testing.T) {
	install(t, queueFake())
	if _, _, err := PlanResume("/r"); err == nil || !strings.Contains(err.Error(), "no stopped merge queue") {
		t.Errorf("PlanResume with no record = %v, want \"no stopped merge queue\"", err)
	}
}

// A record the queue cannot write is said out loud, and never stops a merge:
// the state dir here is a plain file.
func TestMergeQueue_UnwritableRecordWarnsAndStillMerges(t *testing.T) {
	f := queueFake()
	install(t, f)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", notADir)

	items, _ := PlanMergeQueue("/r", []int{21})
	var out, errb bytes.Buffer
	if err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("RunMergeQueue: %v\n%s", err, out.String())
	}
	if got := strings.Join(f.merged, ","); got != "lane/one" {
		t.Errorf("merged %q, want lane/one", got)
	}
	if !strings.Contains(errb.String(), "merge queue record not written") {
		t.Errorf("stderr does not say the record was not written:\n%s", errb.String())
	}
}

// After a reboot the queue's old pid can belong to another process: that
// process is not the queue, so it does not block a resume.
func TestMergeQueue_PidReusedByAnotherProcessDoesNotBlockAResume(t *testing.T) {
	f := queueFake()
	install(t, f)
	reused := &tdd.MergeQueueRecord{Repo: "/r", PRs: []tdd.MergeQueuePR{
		{PR: 21, Status: tdd.MergeQueueMerged},
		{PR: 22, Status: tdd.MergeQueuePending},
	}}
	reused.StampThisProcess()
	reused.Identity = "another-boot:1"
	if err := tdd.SaveMergeQueueRecord(reused); err != nil {
		t.Fatal(err)
	}

	prior, items, err := PlanResume("/r")
	if err != nil {
		t.Fatalf("PlanResume over a reused pid: %v", err)
	}
	var out, errb bytes.Buffer
	if err := ResumeMergeQueue("/r", prior, items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("ResumeMergeQueue: %v\n%s", err, out.String())
	}
	if got := strings.Join(f.merged, ","); got != "lane/two" {
		t.Errorf("merged %q, want lane/two", got)
	}
}
