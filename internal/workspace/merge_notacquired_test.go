package workspace

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Hosted runners sometimes never pick a job up: GitHub cancels it after 15
// minutes with no step run. That is a verdict about the runner pool, not the
// code, and asking again often works, so merge --wait asks twice before it
// calls CI unavailable.

func notAcquiredRun(name, sha string, id int64) CheckRun {
	r := notStartedRun(name, sha)
	r.ID, r.NotAcquired = id, true
	return r
}

func stubRerun(t *testing.T, asked *[]int64) {
	t.Helper()
	o := ghRerunFailed
	ghRerunFailed = func(dir string, run int64) error { *asked = append(*asked, run); return nil }
	t.Cleanup(func() { ghRerunFailed = o })
}

func TestMergeWait_AskAgainForJobsNoHostedRunnerAcquired(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	var asked []int64
	stubRerun(t, &asked)
	pr := &fakePR{number: 21, branch: "lane/na", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 11), notAcquiredRun("test", newSHA, 12)}},
		{head: newSHA, checks: []CheckRun{run("lint", newSHA, "in_progress", ""), run("test", newSHA, "in_progress", "")}},
		{head: newSHA, checks: []CheckRun{run("lint", newSHA, "completed", "success"), run("test", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/na": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/na", Branch: "lane/na", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err != nil {
		t.Fatalf("MergeWait: %v\n%s", err, out.String())
	}
	if len(asked) != 1 || asked[0] != 1 {
		t.Errorf("re-requested runs %v, want run 1 once: both jobs belong to it", asked)
	}
	if n := strings.Count(out.String(), "re-requesting"); n != 1 {
		t.Errorf("printed %d re-request lines, want 1:\n%s", n, out.String())
	}
	if len(f.merged) != 1 {
		t.Errorf("merged %v, want lane/na once", f.merged)
	}
}

func TestMergeWait_AGivenUpJobIsReRequestedTwiceThenCalledUnavailable(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	var asked []int64
	stubRerun(t, &asked)
	pr := &fakePR{number: 22, branch: "lane/nb", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 11)}},
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 12)}},
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 13)}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/nb": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/nb", Branch: "lane/nb", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err == nil || !isCIUnavailable(err) {
		t.Fatalf("err = %v, want ci unavailable after the re-requests ran out", err)
	}
	if len(asked) != 2 {
		t.Errorf("re-requested %v, want exactly 2 asks", asked)
	}
	if len(f.merged) != 0 {
		t.Errorf("merged %v on a head no job judged", f.merged)
	}
}

// The check list right after a re-request can still show the cancelled job; it
// is the same job, not a second failure to ask about.
func TestMergeWait_TheSameCancelledJobSeenAgainIsNotAskedForTwice(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	var asked []int64
	stubRerun(t, &asked)
	pr := &fakePR{number: 23, branch: "lane/nc", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 11)}},
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 11)}},
		{head: newSHA, checks: []CheckRun{run("lint", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/nc": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/nc", Branch: "lane/nc", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err != nil {
		t.Fatalf("MergeWait: %v\n%s", err, out.String())
	}
	if len(asked) != 1 {
		t.Errorf("asked %v, want one ask for the one job", asked)
	}
}

func TestMergeWait_AJobThatWasNotAnOutageOfRunnersIsNeverReRequested(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	var asked []int64
	stubRerun(t, &asked)
	pr := &fakePR{number: 24, branch: "lane/nd", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notStartedRun("lint", newSHA)}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/nd": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/nd", Branch: "lane/nd", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err == nil || !isCIUnavailable(err) {
		t.Fatalf("err = %v, want ci unavailable", err)
	}
	if len(asked) != 0 {
		t.Errorf("re-requested %v a job a billing lock stopped", asked)
	}
}

// Once its asks are spent, a job no runner replaced is an outage, not something
// to poll for until the wait times out: the wait fails as CI unavailable, so the
// ci = auto fallback takes over, and names the job.
func TestMergeWait_AJobNoRunnerEverReplacedEndsTheWaitAsCIUnavailableNamingIt(t *testing.T) {
	pinCIMode(t, tdd.CIGithub)
	var asked []int64
	stubRerun(t, &asked)
	pr := &fakePR{number: 25, branch: "lane/ne", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notAcquiredRun("lint", newSHA, 11)}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/ne": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/ne", Branch: "lane/ne", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err == nil || !isCIUnavailable(err) {
		t.Fatalf("err = %v, want ci unavailable, not a timeout", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("the wait ran to its timeout:\n%v", err)
	}
	if f.slept > 5*time.Minute {
		t.Errorf("waited %v on a job nothing replaced, want a few polls", f.slept)
	}
	if !strings.Contains(out.String(), "lint") || !strings.Contains(out.String(), "no runner took") {
		t.Errorf("no line names the job nothing replaced:\n%s", out.String())
	}
}
