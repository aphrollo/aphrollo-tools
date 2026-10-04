package github

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// The reads below are tested on recordings: what gh answered for each call the
// adapter makes, taken from this repository's own pull requests and runs (see
// testdata/README.md). A call the adapter makes that is not in the recordings
// fails the test, which is how a changed request is noticed.

type recording struct {
	Args  []string `json:"args"`
	File  string   `json:"file"`
	Error string   `json:"error"`
}

var (
	loadOnce   sync.Once
	recordings []recording
	loadErr    error
)

func loadRecordings() ([]recording, error) {
	loadOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join("testdata", "index.json"))
		if err != nil {
			loadErr = err
			return
		}
		loadErr = json.Unmarshal(raw, &recordings)
	})
	return recordings, loadErr
}

// replayRunner answers each call from the recording of the same arguments, and
// remembers what was asked.
type replayRunner struct {
	t     *testing.T
	mu    sync.Mutex
	asked [][]string
}

func (r *replayRunner) run(_ string, _ time.Duration, args ...string) ([]byte, error) {
	r.t.Helper()
	r.mu.Lock()
	r.asked = append(r.asked, args)
	r.mu.Unlock()
	recs, err := loadRecordings()
	if err != nil {
		r.t.Fatalf("recordings: %v", err)
	}
	for _, rec := range recs {
		if slices.Equal(rec.Args, args) {
			data, err := os.ReadFile(filepath.Join("testdata", rec.File))
			if err != nil {
				r.t.Fatalf("recording %s: %v", rec.File, err)
			}
			if rec.Error != "" {
				return data, errors.New(rec.Error)
			}
			return data, nil
		}
	}
	r.t.Fatalf("no recording of gh %s", strings.Join(args, " "))
	return nil, nil
}

func replayHost(t *testing.T) (*GitHub, *replayRunner) {
	t.Helper()
	r := &replayRunner{t: t}
	g := New(Options{Dir: t.TempDir(), Runner: r.run,
		Origin: func() string { return "https://github.com/aphrollo/aphrollo-tools.git" }})
	return g, r
}

const (
	mergedPR  = 1202
	mergedSHA = "6e2087329a8fcdf466b3d73eddbed4bd1328ed2f"
	redRun    = int64(37167140462)
)

func TestRecorded_APullRequestIsReadByBranchWithItsHeadAndBase(t *testing.T) {
	g, _ := replayHost(t)

	pr, err := g.PRByBranch("lane/measure-fixes")

	if err != nil || pr == nil {
		t.Fatalf("PRByBranch = %+v, %v", pr, err)
	}
	if pr.Number != mergedPR || pr.State != "MERGED" || pr.HeadSHA != mergedSHA || pr.HeadRef != "lane/measure-fixes" {
		t.Errorf("pr = %+v, want #%d MERGED at %s", pr, mergedPR, mergedSHA)
	}
	if pr.BaseRef != "main" || pr.BaseRepo != "aphrollo/aphrollo-tools" {
		t.Errorf("base = %s in %s, want main in aphrollo/aphrollo-tools", pr.BaseRef, pr.BaseRepo)
	}
	if pr.Mergeable != "UNKNOWN" {
		t.Errorf("a merged PR has no mergeable verdict: got %q, want UNKNOWN", pr.Mergeable)
	}
}

func TestRecorded_ABranchWithNoPullRequestIsNilNotAnError(t *testing.T) {
	g, _ := replayHost(t)

	pr, err := g.PRByBranch("lane/no-such-branch-recorded")

	if err != nil || pr != nil {
		t.Fatalf("PRByBranch = %+v, %v; want nil, nil", pr, err)
	}
}

func TestRecorded_APullRequestByNumberOrByBranchIsTheSameRead(t *testing.T) {
	g, r := replayHost(t)

	byRef, err := g.PRByRef("1202")
	if err != nil || byRef == nil || byRef.HeadSHA != mergedSHA {
		t.Fatalf("PRByRef(1202) = %+v, %v", byRef, err)
	}
	if len(r.asked) != 1 || r.asked[0][0] != "api" {
		t.Errorf("a number is one REST read, got %v", r.asked)
	}
}

func TestRecorded_ChecksOfACommitCarryEachRunsIdentityAndConclusion(t *testing.T) {
	g, _ := replayHost(t)

	checks, err := g.ChecksAt(mergedSHA)

	if err != nil || len(checks) == 0 {
		t.Fatalf("ChecksAt = %d checks, %v", len(checks), err)
	}
	for _, c := range checks {
		if c.SHA != mergedSHA {
			t.Errorf("check %q is on %s, not the commit asked about", c.Name, c.SHA)
		}
	}
	idx := slices.IndexFunc(checks, func(c host.Check) bool { return c.Name == "build" })
	if idx < 0 || checks[idx].App != "github-actions" || checks[idx].Conclusion != "success" || checks[idx].ID == 0 {
		t.Errorf("the build check = %+v, want a github-actions success with its id", checks[idx])
	}
}

func TestRecorded_ABaseWithAMergeQueueRuleHasAQueue(t *testing.T) {
	ResetQueueRules()
	g, r := replayHost(t)

	for range 2 {
		queued, err := g.HasMergeQueue("", "main")
		if err != nil || !queued {
			t.Fatalf("HasMergeQueue = %v, %v; want true", queued, err)
		}
	}
	if len(r.asked) != 1 {
		t.Errorf("rules read %d times, want once: the answer is kept", len(r.asked))
	}
}

func TestRecorded_AMergedPRIsInNoQueueAndItsTimelineSaysMerged(t *testing.T) {
	g, _ := replayHost(t)

	entry, err := g.QueueEntry("", mergedPR)
	if err != nil || entry != nil {
		t.Fatalf("QueueEntry = %+v, %v; want nil, nil for a merged PR", entry, err)
	}
	rem, err := g.QueueRemoval("", mergedPR)
	if err != nil || rem.Removed || rem.Reason != "merged" || rem.FailedChecks {
		t.Errorf("QueueRemoval = %+v, %v; want the queue's merge, not a drop", rem, err)
	}
}

func TestRecorded_TheMergeGroupRunOfAQueuedPRIsFound(t *testing.T) {
	g, _ := replayHost(t)

	id, err := g.MergeGroupRun("", mergedPR)

	if err != nil || id != 37168149295 {
		t.Fatalf("MergeGroupRun = %d, %v; want 37168149295", id, err)
	}
}

func TestRecorded_ARunsPullRequestRunsAreKeptByThePRThatOwnsThem(t *testing.T) {
	g, _ := replayHost(t)

	runs, err := g.PRRuns("lane/f21-host-port", 1204)

	if err != nil || len(runs) != 2 {
		t.Fatalf("PRRuns = %+v, %v; want the PR's two runs", runs, err)
	}
	for _, r := range runs {
		if r.SHA == "" || r.Status != "completed" || r.Attempt != 1 || r.CreatedAt == "" {
			t.Errorf("run = %+v, want every field read", r)
		}
	}
}

func TestRecorded_ARedRunNamesItsFailedJobAndHowItsFirstAttemptEnded(t *testing.T) {
	g, _ := replayHost(t)

	names, err := g.RunFailedJobs(redRun, 1)
	if err != nil || !slices.Equal(names, []string{"test-windows (rest)"}) {
		t.Fatalf("RunFailedJobs = %q, %v", names, err)
	}
	status, conclusion, err := g.RunFirstAttempt(redRun)
	if err != nil || status != "completed" || conclusion != "failure" {
		t.Errorf("RunFirstAttempt = %s %s, %v; want completed failure", status, conclusion, err)
	}
	info, err := g.RunInfo(redRun)
	if err != nil || info.Workflow != "pipeline.yml" || info.Attempt != 1 {
		t.Errorf("RunInfo = %+v, %v; want pipeline.yml attempt 1", info, err)
	}
}

func TestRecorded_ARunIsReadWithItsJobsAndTheFailedJobsEvidence(t *testing.T) {
	g, _ := replayHost(t)

	run, err := g.Run(redRun)
	if err != nil || run == nil {
		t.Fatalf("Run = %+v, %v", run, err)
	}
	if run.Conclusion != "failure" || run.Workflow != "Pipeline" || run.Attempt != 1 || run.ID != redRun {
		t.Errorf("run = %+v, want a failed Pipeline attempt 1", run)
	}
	var failed *host.Job
	for i := range run.Jobs {
		if run.Jobs[i].Conclusion == "failure" {
			failed = &run.Jobs[i]
		}
	}
	if failed == nil || failed.Name != "test-windows (rest)" || len(failed.Steps) == 0 {
		t.Fatalf("failed job = %+v, want test-windows (rest) with its steps", failed)
	}
	anns, err := g.JobAnnotations(failed.ID)
	if err != nil || !slices.Equal(anns, []string{"Process completed with exit code 1."}) {
		t.Errorf("JobAnnotations = %q, %v", anns, err)
	}
	log, err := g.JobLog(failed.ID)
	if err != nil || len(log) == 0 {
		t.Errorf("JobLog = %d bytes, %v; want the failed-step log", len(log), err)
	}
	whole, err := g.RunLog(redRun)
	if err != nil || len(whole) == 0 {
		t.Errorf("RunLog = %d bytes, %v", len(whole), err)
	}
}

func TestRecorded_ATargetBecomesARunIdFromMainFromAPRAndFromTheNumberItself(t *testing.T) {
	g, _ := replayHost(t)

	byMain, err := g.ResolveRun(host.RunTarget{Main: true, Workflow: "Pipeline"})
	if err != nil || byMain != 37168533038 {
		t.Errorf("main = %d, %v; want 37168533038", byMain, err)
	}
	byPR, err := g.ResolveRun(host.RunTarget{PR: mergedPR, Workflow: "Pipeline"})
	if err != nil || byPR != 37167698371 {
		t.Errorf("PR = %d, %v; want 37167698371", byPR, err)
	}
	byID, err := g.ResolveRun(host.RunTarget{Run: 42})
	if err != nil || byID != 42 {
		t.Errorf("run id = %d, %v; want it as given", byID, err)
	}
}

func TestRecorded_ABranchsRunsAreListedNewestFirstForTheRetro(t *testing.T) {
	g, _ := replayHost(t)

	runs, err := g.RunsOn("lane/measure-fixes", "pull_request", 20)

	if err != nil || len(runs) != 4 {
		t.Fatalf("RunsOn = %d runs, %v; want the four recorded", len(runs), err)
	}
	if runs[0].ID != 37167698371 || runs[0].Conclusion != "success" || runs[0].Attempt != 1 || runs[0].CreatedAt.IsZero() {
		t.Errorf("newest run = %+v", runs[0])
	}
}

func TestRecorded_ASummaryCarriesTheTimesTheRetroMeasures(t *testing.T) {
	g, _ := replayHost(t)

	s, err := g.Summary(mergedPR)

	if err != nil || s == nil || s.HeadRef != "lane/measure-fixes" || s.MergedAt.IsZero() || !s.MergedAt.After(s.CreatedAt) {
		t.Fatalf("Summary = %+v, %v", s, err)
	}
}

func TestRecorded_ThePRTextIsTheTitleAndBodyAsTheyLanded(t *testing.T) {
	g, _ := replayHost(t)

	title, body, err := g.PRText("lane/f21-host-port")

	if err != nil || title == "" || body == "" {
		t.Fatalf("PRText = %q (%d bytes of body), %v", title, len(body), err)
	}
}

func TestExecRunner_AnAbsurdlyShortDeadlineNamesTheStalledCall(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Fatalf("this test needs a gh on PATH to run: %v", err)
	}

	_, err := ExecRunner(t.TempDir(), time.Nanosecond, "api", "user")

	if err == nil || !strings.Contains(err.Error(), "timed out after 1ns") || !strings.Contains(err.Error(), "api user") {
		t.Fatalf("err = %v, want the stalled call and its deadline named", err)
	}
}

// The no-queue rule at the port (#1203, escape #1206): a 404 and a Free plan's
// 403 are no queue; a 403 about the token is a refusal with the fix.
func TestRecorded_ABranchRulesReadThatSaysNoRulesetsIsNoQueueAndATokenProblemIsARefusal(t *testing.T) {
	for _, c := range []struct {
		repo      string
		wantQueue bool
		wantErr   string
	}{
		{"aphrollo/no-such-repo-recorded", false, ""},
		{"free-org/private-repo", false, ""},
		{"scoped-org/token-repo", false, "gh auth status"},
	} {
		ResetQueueRules()
		g, _ := replayHost(t)

		queued, err := g.HasMergeQueue(c.repo, "main")

		if c.wantErr == "" && (err != nil || queued != c.wantQueue) {
			t.Errorf("%s: HasMergeQueue = %v, %v; want no queue and no error", c.repo, queued, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: err = %v, want a refusal carrying %q", c.repo, err, c.wantErr)
		}
	}
}
