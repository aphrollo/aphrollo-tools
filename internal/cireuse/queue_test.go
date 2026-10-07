package cireuse

import (
	"errors"
	"strings"
	"testing"
)

const baseSHA = "4444444444444444444444444444444444444444"

// queuedPRSource is a merge group of the one pull request #42, whose pull request
// run was green on a tree equal to the group's.
func queuedPRSource() *fakeSource {
	src := greenSource()
	src.pulls = nil
	src.pull = Pull{Number: 42, HeadSHA: headSHA}
	return src
}

func queueCommit() Commit {
	return Commit{Repo: "o/r", SHA: pushSHA, Tree: pushTree, Workflow: wfPath, Event: "merge_group",
		HeadRef: "gh-readonly-queue/main/pr-42-" + headSHA, BaseSHA: baseSHA, Parent: baseSHA}
}

func decideGroup(src *fakeSource, edit func(*Commit)) Verdict {
	c := queueCommit()
	if edit != nil {
		edit(&c)
	}
	return Decide(src, c, testReqs)
}

func TestDecideQueue_ReusesTheOnePullRequestsRunWhenTheGroupsTreeEqualsItsTestedTree(t *testing.T) {
	t.Parallel()
	src := queuedPRSource()
	got := decideGroup(src, nil)
	if !got.Reuse {
		t.Fatalf("reuse = false (%s), want true", got.Reason)
	}
	for _, want := range []string{"#42", "https://github.com/o/r/actions/runs/900", pushTree} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
	if src.pullAsked != 42 {
		t.Errorf("pull #%d was read, want #42 from the head ref", src.pullAsked)
	}
	if len(src.runsAsked) != 1 || src.runsAsked[0] != "pull_request:"+headSHA {
		t.Errorf("runs asked %v, want only the pull request's run on its head", src.runsAsked)
	}
}

func TestDecideQueue_RunsEverythingWhenAnyEvidenceIsMissing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		src    func(*fakeSource)
		commit func(*Commit)
		reason string
		never  string // a lookup the verdict must not have gone to
	}{
		{"the queue's tree differs from the tested one", func(s *fakeSource) { s.tree = "9" + pushTree[1:] }, nil, "differs", ""},
		{"the group holds more than one pull request", nil, func(c *Commit) { c.Parent = "9" + baseSHA[1:] }, "more than one", "pull"},
		{"the group's parent is unknown", nil, func(c *Commit) { c.Parent = "" }, "more than one", "pull"},
		{"the group's base is unknown", nil, func(c *Commit) { c.BaseSHA = "" }, "more than one", "pull"},
		{"the head ref names no pull request", nil, func(c *Commit) { c.HeadRef = "refs/heads/lane/x" }, "head ref", "pull"},
		{"the head ref names a pull request without a number", nil, func(c *Commit) { c.HeadRef = "gh-readonly-queue/main/pr-x-" + headSHA }, "head ref", "pull"},
		{"the pull request moved on since it was queued", func(s *fakeSource) { s.pull.HeadSHA = "9" + headSHA[1:] }, nil, "moved", "runs:pull_request"},
		{"no pipeline run on the head", func(s *fakeSource) { s.runs = nil }, nil, "no pipeline run", "jobs"},
		{"the run was re-run", func(s *fakeSource) { s.runs[0].Attempt = 2 }, nil, "attempt", "jobs"},
		{"the run is red", func(s *fakeSource) { s.runs[0].Conclusion = "failure" }, nil, "not success", "jobs"},
		{"a test step was skipped", func(s *fakeSource) { s.jobs[0].Steps[1].Conclusion = "skipped" }, nil, "`test`", "tree"},
		{"a required job is absent", func(s *fakeSource) { s.jobs = s.jobs[:1] }, nil, "`test-windows`", "tree"},
		{"the run published no tree", func(s *fakeSource) { s.treeErr = errors.New("no artifact named tested-tree") }, nil, "no artifact", ""},
		{"the pull request cannot be read", func(s *fakeSource) { s.pullErr = errors.New("HTTP 502") }, nil, "HTTP 502", "runs:pull_request"},
		{"the runs cannot be read", func(s *fakeSource) { s.runsErr = errors.New("HTTP 502") }, nil, "HTTP 502", "jobs"},
		{"the jobs cannot be read", func(s *fakeSource) { s.jobsErr = errors.New("HTTP 502") }, nil, "HTTP 502", "tree"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := queuedPRSource()
			if c.src != nil {
				c.src(src)
			}
			got := decideGroup(src, c.commit)
			if got.Reuse {
				t.Fatalf("reuse = true, want false: %s", got.Reason)
			}
			if !strings.Contains(got.Reason, c.reason) {
				t.Errorf("reason %q does not say %q", got.Reason, c.reason)
			}
			if c.never != "" && src.asked[c.never] {
				t.Errorf("went on to look up %q after the verdict was already no", c.never)
			}
		})
	}
}

// The push path looks for the queue's own run of the head; a queue run reuses
// the pull request's, and asking for merge_group runs there would find itself.
func TestDecideQueue_NeverLooksForMergeGroupRuns(t *testing.T) {
	t.Parallel()
	src := queuedPRSource()
	decideGroup(src, nil)
	for _, asked := range src.runsAsked {
		if strings.HasPrefix(asked, "merge_group:") {
			t.Errorf("asked for %q", asked)
		}
	}
}

func TestDecide_APushIsUnchangedByTheQueueFields(t *testing.T) {
	t.Parallel()
	got := Decide(greenSource(), Commit{Repo: "o/r", SHA: pushSHA, Tree: pushTree, Workflow: wfPath, Event: "push"}, testReqs)
	want := decide(greenSource())
	if got != want {
		t.Errorf("verdict with Event push = %+v, want the same as with no event: %+v", got, want)
	}
}
