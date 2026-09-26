package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// goneQueuePID is a pid no process can hold: above every platform's pid ceiling.
const goneQueuePID = 1<<31 - 2

// `aphrollo status` in a repository whose merge queue died with its session
// names the queue and the command that resumes it.
func TestStatus_ReportsAMergeQueueWhoseProcessIsGone(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	inDir(t, repo)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rec := &tdd.MergeQueueRecord{Repo: cwd, PID: goneQueuePID, PRs: []tdd.MergeQueuePR{
		{PR: 5, Status: tdd.MergeQueueMerged},
		{PR: 6, Status: tdd.MergeQueuePending},
	}}
	if err := tdd.SaveMergeQueueRecord(rec); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"status"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\nstderr: %s", code, errb.String())
	}
	want := "merge queue for #5 #6 stopped at #6 (process gone) — resume: aphrollo workspace merge --wait --resume"
	if !strings.Contains(out.String(), want) {
		t.Errorf("status does not report the stopped queue; want %q in:\n%s", want, out.String())
	}
}

// --resume only means something to the waiting queue.
func TestWorkspaceMerge_ResumeWithoutWaitIsAUsageError(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "merge", "--resume"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--resume needs --wait") {
		t.Errorf("stderr = %q, want it to say --resume needs --wait", errb.String())
	}
}

func TestWorkspaceMerge_ResumeTakesNoPRNumbers(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "merge", "--wait", "--resume", "12"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--resume takes no PR numbers") {
		t.Errorf("stderr = %q, want it to say --resume takes no PR numbers", errb.String())
	}
}

func mergeResume(t *testing.T, extra ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(append([]string{"workspace", "merge", "--wait", "--resume"}, extra...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestWorkspaceMerge_WaitResumeWithNoRecordSaysThereIsNothingToResume(t *testing.T) {
	gateConfigDir(t)
	inDir(t, gitInit(t, map[string]string{"a.txt": "a\n"}))
	code, _, stderr := mergeResume(t)
	if code != 1 || !strings.Contains(stderr, "no stopped merge queue") {
		t.Fatalf("exit = %d, stderr = %q; want 1 and \"no stopped merge queue\"", code, stderr)
	}
}

// A queue stamped by this very process is live, so a resume is refused.
func TestWorkspaceMerge_WaitResumeRefusesWhileTheQueueRuns(t *testing.T) {
	gateConfigDir(t)
	repo := gitInit(t, map[string]string{"a.txt": "a\n"})
	inDir(t, repo)
	rec := &tdd.MergeQueueRecord{Repo: repo, PRs: []tdd.MergeQueuePR{{PR: 6, Status: tdd.MergeQueuePending}}}
	rec.StampThisProcess()
	if err := tdd.SaveMergeQueueRecord(rec); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := mergeResume(t)
	if code != 1 || !strings.Contains(stderr, "already runs as pid") {
		t.Fatalf("exit = %d, stderr = %q; want 1 and a refusal naming the live pid", code, stderr)
	}
}

// --dry prints the resumed plan and leaves the record for the real run.
func TestWorkspaceMerge_WaitResumeDryPrintsThePendingPRsAndKeepsTheRecord(t *testing.T) {
	gateConfigDir(t)
	repo := gitInit(t, map[string]string{"a.txt": "a\n"})
	inDir(t, repo)
	rec := &tdd.MergeQueueRecord{Repo: repo, PID: goneQueuePID, PRs: []tdd.MergeQueuePR{
		{PR: 5, Status: tdd.MergeQueueMerged},
		{PR: 6, Status: tdd.MergeQueuePending},
	}}
	if err := tdd.SaveMergeQueueRecord(rec); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := mergeResume(t, "--dry")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "1 PR(s)") || !strings.Contains(stdout, "#6") || strings.Contains(stdout, "#5 ") {
		t.Errorf("plan should list only the pending #6:\n%s", stdout)
	}
	if kept, err := tdd.LoadMergeQueueRecord(repo); err != nil || kept == nil {
		t.Errorf("--dry removed the record (err %v)", err)
	}
}
