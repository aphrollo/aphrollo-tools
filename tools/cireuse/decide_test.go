package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeSource answers what GitHub would, from fields a test sets.
type fakeSource struct {
	pulls     []Pull
	runs      []Run
	jobs      []Job
	tree      string
	trees     map[int64]string // the tree a run recorded; tree answers for any other
	pullsErr  error
	runsErr   error
	jobsErr   error
	treeErr   error
	asked     map[string]bool
	jobsRunID int64
	// runsAsked lists each Runs lookup as "event:head sha", in order.
	runsAsked []string
}

func (f *fakeSource) Pulls(sha string) ([]Pull, error) {
	f.note("pulls")
	return f.pulls, f.pullsErr
}

func (f *fakeSource) Runs(event, headSHA string) ([]Run, error) {
	f.note("runs:" + event)
	f.runsAsked = append(f.runsAsked, event+":"+headSHA)
	return f.runs, f.runsErr
}

func (f *fakeSource) Jobs(runID int64) ([]Job, error) {
	f.note("jobs")
	f.jobsRunID = runID
	return f.jobs, f.jobsErr
}

func (f *fakeSource) TestedTree(runID int64) (string, error) {
	f.note("tree")
	if t, ok := f.trees[runID]; ok {
		return t, f.treeErr
	}
	if f.trees != nil {
		return "", f.treeErr
	}
	return f.tree, f.treeErr
}

func (f *fakeSource) note(what string) {
	if f.asked == nil {
		f.asked = map[string]bool{}
	}
	f.asked[what] = true
}

const (
	pushSHA  = "1111111111111111111111111111111111111111"
	headSHA  = "2222222222222222222222222222222222222222"
	pushTree = "3333333333333333333333333333333333333333"
	wfPath   = ".github/workflows/pipeline.yml"
)

var testReqs = []Requirement{
	{Job: "test", StepPrefix: "Test (race"},
	{Job: "test-windows", StepPrefix: "Test (race"},
}

func green(job, step string) Job {
	return Job{Name: job, Conclusion: "success", Steps: []Step{
		{Name: "Set up job", Conclusion: "success"},
		{Name: step, Conclusion: "success"},
	}}
}

// greenSource is a push whose PR #42 ran the pipeline once, green, on the tree
// the push holds.
func greenSource() *fakeSource {
	return &fakeSource{
		pulls: []Pull{{Number: 42, MergedAt: "2026-10-03T10:00:00Z", MergeCommitSHA: pushSHA, HeadSHA: headSHA}},
		runs: []Run{{ID: 900, Number: 7, Attempt: 1, Event: "pull_request", Path: wfPath,
			Status: "completed", Conclusion: "success", HeadSHA: headSHA, HeadRepo: "o/r",
			URL: "https://github.com/o/r/actions/runs/900"}},
		jobs: []Job{
			green("test", "Test (race + shuffle)"),
			green("test-windows (cli)", "Test (race + shuffle), shard cli"),
			green("test-windows (rest)", "Test (race + shuffle), shard rest"),
			{Name: "scan", Conclusion: "success"},
		},
		tree: pushTree,
	}
}

func decide(src *fakeSource) Verdict {
	return Decide(src, Commit{Repo: "o/r", SHA: pushSHA, Tree: pushTree, Workflow: wfPath}, testReqs)
}

func TestDecide_ReusesWhenThePRRanGreenOnTheSameTree(t *testing.T) {
	t.Parallel()
	src := greenSource()
	got := decide(src)
	if !got.Reuse {
		t.Fatalf("reuse = false (%s), want true", got.Reason)
	}
	for _, want := range []string{"#42", "https://github.com/o/r/actions/runs/900", pushTree} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q, so the summary cannot say whose verdict was reused", got.Reason, want)
		}
	}
	if src.jobsRunID != 900 {
		t.Errorf("jobs were read for run %d, want 900", src.jobsRunID)
	}
}

func TestDecide_RunsEverythingWhenAnyEvidenceIsMissing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*fakeSource)
		reason  string
		asksFor string // the lookup the verdict must not have gone past
	}{
		{"no pull request carries the commit", func(s *fakeSource) { s.pulls = nil }, "no merged pull request", "runs:pull_request"},
		{"the pull request merged as another commit", func(s *fakeSource) { s.pulls[0].MergeCommitSHA = "9" + pushSHA[1:] }, "no merged pull request", "runs:pull_request"},
		{"the pull request never merged", func(s *fakeSource) { s.pulls[0].MergedAt = "" }, "no merged pull request", "runs:pull_request"},
		{"no pipeline run for the head", func(s *fakeSource) { s.runs = nil }, "no pipeline run", "jobs"},
		{"the only run is another event", func(s *fakeSource) { s.runs[0].Event = "push" }, "no pipeline run", "jobs"},
		{"the only run is another workflow file", func(s *fakeSource) { s.runs[0].Path = ".github/workflows/other.yml" }, "no pipeline run", "jobs"},
		{"the only run is for another head", func(s *fakeSource) { s.runs[0].HeadSHA = "9" + headSHA[1:] }, "no pipeline run", "jobs"},
		{"the run came from a fork", func(s *fakeSource) { s.runs[0].HeadRepo = "fork/r" }, "no pipeline run", "jobs"},
		{"the run was re-run", func(s *fakeSource) { s.runs[0].Attempt = 2 }, "attempt", "jobs"},
		{"the run is red", func(s *fakeSource) { s.runs[0].Conclusion = "failure" }, "not success", "jobs"},
		{"the run is still going", func(s *fakeSource) { s.runs[0].Status = "in_progress"; s.runs[0].Conclusion = "" }, "not success", "jobs"},
		{"a test job is red", func(s *fakeSource) { s.jobs[0].Conclusion = "failure" }, "`test`", "tree"},
		{"a test job never ran its test step", func(s *fakeSource) { s.jobs[0].Steps[1].Conclusion = "skipped" }, "`test`", "tree"},
		{"a test job has no test step", func(s *fakeSource) { s.jobs[0].Steps = s.jobs[0].Steps[:1] }, "`test`", "tree"},
		{"one windows shard is red", func(s *fakeSource) { s.jobs[2].Conclusion = "failure" }, "`test-windows (rest)`", "tree"},
		{"one windows shard skipped its test step", func(s *fakeSource) { s.jobs[2].Steps[1].Conclusion = "skipped" }, "`test-windows (rest)`", "tree"},
		{"a required job is absent", func(s *fakeSource) { s.jobs = s.jobs[:1] }, "`test-windows`", "tree"},
		{"trunk moved so the tested tree differs", func(s *fakeSource) { s.tree = "9" + pushTree[1:] }, "differs", ""},
		{"the run recorded no tree", func(s *fakeSource) { s.tree = "" }, "differs", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := greenSource()
			c.mutate(src)
			got := decide(src)
			if got.Reuse {
				t.Fatalf("reuse = true, want false: %s", got.Reason)
			}
			if !strings.Contains(got.Reason, c.reason) {
				t.Errorf("reason %q does not say %q", got.Reason, c.reason)
			}
			if c.asksFor != "" && src.asked[c.asksFor] {
				t.Errorf("went on to look up %q after the verdict was already no", c.asksFor)
			}
		})
	}
}

func TestDecide_AnUnreadableSourceNeverReuses(t *testing.T) {
	t.Parallel()
	boom := errors.New("HTTP 502")
	cases := map[string]func(*fakeSource){
		"pulls": func(s *fakeSource) { s.pullsErr = boom },
		"runs":  func(s *fakeSource) { s.runsErr = boom },
		"jobs":  func(s *fakeSource) { s.jobsErr = boom },
		"tree":  func(s *fakeSource) { s.treeErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := greenSource()
			mutate(src)
			got := decide(src)
			if got.Reuse {
				t.Fatal("reuse = true over a lookup that failed")
			}
			if !strings.Contains(got.Reason, "HTTP 502") {
				t.Errorf("reason %q hides the lookup error", got.Reason)
			}
		})
	}
}

func TestDecide_TheNewestRunOfTheHeadDecides(t *testing.T) {
	t.Parallel()
	src := greenSource()
	// An older green run of the same head must not vouch for a newer red one.
	src.runs = []Run{
		{ID: 901, Number: 8, Attempt: 1, Event: "pull_request", Path: wfPath, Status: "completed",
			Conclusion: "failure", HeadSHA: headSHA, HeadRepo: "o/r", URL: "u901"},
		src.runs[0],
	}
	if got := decide(src); got.Reuse {
		t.Fatalf("reuse = true behind a newer red run: %s", got.Reason)
	}
	src = greenSource()
	src.runs = []Run{
		{ID: 899, Number: 6, Attempt: 1, Event: "pull_request", Path: wfPath, Status: "completed",
			Conclusion: "failure", HeadSHA: headSHA, HeadRepo: "o/r", URL: "u899"},
		src.runs[0],
	}
	if got := decide(src); !got.Reuse {
		t.Fatalf("an older red run blocked the newer green one: %s", got.Reason)
	}
}

func TestDecide_AWorkflowPathCarryingARefStillMatches(t *testing.T) {
	t.Parallel()
	src := greenSource()
	src.runs[0].Path = wfPath + "@refs/pull/42/merge"
	if got := decide(src); !got.Reuse {
		t.Fatalf("reuse = false (%s): GitHub spells some run paths with an @ref suffix", got.Reason)
	}
}

func TestDecide_RequiresAtLeastOneCheck(t *testing.T) {
	t.Parallel()
	got := Decide(greenSource(), Commit{Repo: "o/r", SHA: pushSHA, Tree: pushTree, Workflow: wfPath}, nil)
	if got.Reuse {
		t.Fatal("reuse = true with no required check, which would trust a run for nothing it proved")
	}
}

// queueSource is a push that fast-forwarded main to a merge group's head: the
// queue's own run is green on that very commit and tree, while the merged pull
// request's run tested an older base, so its tree is another one.
func queueSource() *fakeSource {
	src := greenSource()
	src.runs = append(src.runs, Run{ID: 950, Number: 9, Attempt: 1, Event: "merge_group", Path: wfPath,
		Status: "completed", Conclusion: "success", HeadSHA: pushSHA, HeadRepo: "o/r",
		URL: "https://github.com/o/r/actions/runs/950"})
	src.tree = "9" + pushTree[1:] // what the pull request run recorded
	src.trees = map[int64]string{950: pushTree}
	return src
}

func TestDecide_ReusesTheMergeGroupRunOfTheVeryCommit(t *testing.T) {
	t.Parallel()
	src := queueSource()
	got := decide(src)
	if !got.Reuse {
		t.Fatalf("reuse = false (%s), want true", got.Reason)
	}
	for _, want := range []string{"merge group", "https://github.com/o/r/actions/runs/950", pushTree} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
	if src.jobsRunID != 950 {
		t.Errorf("jobs were read for run %d, want the merge group's 950", src.jobsRunID)
	}
	if want := []string{"merge_group:" + pushSHA}; !reflect.DeepEqual(src.runsAsked, want) {
		t.Errorf("runs asked = %v, want %v: the group's run is found by the pushed commit itself", src.runsAsked, want)
	}
	if src.asked["pulls"] {
		t.Error("looked up the merged pull request although the group's run decides")
	}
}

// A queue group of several pull requests lands several commits and only the
// last is the group's head: an earlier one has no merge_group run of its own,
// and the pull request run it falls back to tested another base.
func TestDecide_AnIntermediateCommitOfAGroupNeverReuses(t *testing.T) {
	t.Parallel()
	src := queueSource()
	src.runs[1].HeadSHA = "8" + pushSHA[1:] // the group's head is a later commit
	got := decide(src)
	if got.Reuse {
		t.Fatalf("reuse = true for a commit no queue run tested: %s", got.Reason)
	}
	if !strings.Contains(got.Reason, "differs") {
		t.Errorf("reason %q does not say the pull request run tested another tree", got.Reason)
	}
}

// The group's run is held to every rule a pull request run is.
func TestDecide_AMergeGroupRunNeedsTheSameEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*fakeSource)
		reason string
	}{
		{"re-run", func(s *fakeSource) { s.runs[1].Attempt = 2 }, "attempt"},
		{"red", func(s *fakeSource) { s.runs[1].Conclusion = "failure" }, "not success"},
		{"still going", func(s *fakeSource) { s.runs[1].Status = "in_progress"; s.runs[1].Conclusion = "" }, "not success"},
		{"a test job red", func(s *fakeSource) { s.jobs[0].Conclusion = "failure" }, "`test`"},
		{"a windows shard skipped its step", func(s *fakeSource) { s.jobs[1].Steps[1].Conclusion = "skipped" }, "`test-windows (cli)`"},
		{"another workflow file", func(s *fakeSource) { s.runs[1].Path = ".github/workflows/other.yml" }, "differs"},
		{"from a fork", func(s *fakeSource) { s.runs[1].HeadRepo = "fork/r" }, "differs"},
		{"recorded another tree", func(s *fakeSource) { s.trees[950] = "9" + pushTree[1:] }, "differs"},
		{"recorded no tree", func(s *fakeSource) { delete(s.trees, 950) }, "differs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := queueSource()
			c.mutate(src)
			got := decide(src)
			if got.Reuse {
				t.Fatalf("reuse = true: %s", got.Reason)
			}
			if !strings.Contains(got.Reason, c.reason) {
				t.Errorf("reason %q does not say %q", got.Reason, c.reason)
			}
		})
	}
}

func TestDecide_AMergeGroupRunWithAFailedLookupNeverReuses(t *testing.T) {
	t.Parallel()
	src := queueSource()
	src.treeErr = errors.New("HTTP 404")
	if got := decide(src); got.Reuse || !strings.Contains(got.Reason, "HTTP 404") {
		t.Fatalf("verdict = %+v, want no, naming the lookup error", got)
	}
}
