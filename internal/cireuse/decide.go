package cireuse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Pull is the part of a pull request the verdict reads.
type Pull struct {
	Number         int
	MergedAt       string
	MergeCommitSHA string
	HeadSHA        string
}

// Run is one pipeline workflow run.
type Run struct {
	ID         int64
	Number     int
	Attempt    int
	Event      string
	Path       string
	Status     string
	Conclusion string
	HeadSHA    string
	HeadRepo   string
	URL        string
}

// Step is one step of a job.
type Step struct {
	Name       string
	Conclusion string
}

// Job is one job of a run with its steps.
type Job struct {
	Name       string
	Conclusion string
	Steps      []Step
}

// Requirement names a job, and the step of it that does the testing: every job
// called Job (or "Job (<matrix>)") must have concluded success and have run a
// step whose name starts with StepPrefix to success. A job skipped by the
// class filter concludes success with its steps skipped, which is why the step
// is checked and the job's own conclusion is not enough.
type Requirement struct {
	Job        string
	StepPrefix string
}

// Commit is the commit the verdict is about: the one pushed to trunk, or the
// head of a merge group.
type Commit struct {
	Repo     string
	SHA      string
	Tree     string
	Workflow string
	// Event is "push" (or empty, the same) or "merge_group". The rest is read
	// only for a merge group: its head ref, the base it was built on, and the
	// head commit's first parent, which is that base exactly when the group
	// holds one pull request.
	Event   string
	HeadRef string
	BaseSHA string
	Parent  string
}

// Verdict is whether the push may take the pull request's CI verdict for its
// own tree, and the one line saying why or why not.
type Verdict struct {
	Reuse  bool
	Reason string
}

// Source is what GitHub knows. The decision reads nothing else.
type Source interface {
	// Pulls lists the pull requests associated with a commit.
	Pulls(sha string) ([]Pull, error)
	// Pull reads one pull request by number.
	Pull(number int) (Pull, error)
	// Runs lists the pipeline runs of one event (pull_request or merge_group)
	// whose head commit is headSHA.
	Runs(event, headSHA string) ([]Run, error)
	// Jobs lists the jobs of the run's first attempt.
	Jobs(runID int64) ([]Job, error)
	// TestedTree is the tree the run's checkout tested, as the run recorded it.
	TestedTree(runID int64) (string, error)
}

func no(format string, args ...any) Verdict {
	return Verdict{Reason: fmt.Sprintf(format, args...)}
}

// Decide answers whether the push to trunk of c may skip the suites because
// a pipeline run already tested this very tree and passed: the merge queue's
// own run when the push fast-forwarded main to a merge group's head, else the
// merged pull request's run. Every doubt, including a lookup that failed, is a
// no: the full suite runs.
func Decide(src Source, c Commit, reqs []Requirement) Verdict {
	if len(reqs) == 0 {
		return no("no check is required of the run")
	}
	if c.Event == "merge_group" {
		return decideQueue(src, c, reqs)
	}
	// The queue fast-forwards main to the group's head, so the pushed commit is
	// the head sha of the group's run. A group of several pull requests lands
	// several commits and only the last is that head; an earlier one has no run
	// of its own here and falls to the pull request path, whose run tested
	// another base and so another tree.
	runs, err := src.Runs("merge_group", c.SHA)
	if err != nil {
		return no("the merge group runs of %s could not be read: %v", c.SHA, err)
	}
	groupWhy := ""
	if run, ok := newestRun(runs, "merge_group", c.SHA, c); ok {
		v := judge(src, c, reqs, run, fmt.Sprintf("the merge group that landed %s", c.SHA))
		if v.Reuse {
			return v
		}
		// A group that itself reused a pull request's verdict ran none of the
		// suites, so it fails the rules above; its tree is then the pull
		// request's tested tree, and the pull request's run is held to the
		// same rules below. Both reasons are kept when neither vouches.
		groupWhy = v.Reason + "; falling back to the pull request: "
	}
	v := decidePull(src, c, reqs)
	v.Reason = groupWhy + v.Reason
	return v
}

// decidePull is the pull request path of Decide: the merged pull request's run
// on its head.
func decidePull(src Source, c Commit, reqs []Requirement) Verdict {
	pulls, err := src.Pulls(c.SHA)
	if err != nil {
		return no("the pull requests of %s could not be read: %v", c.SHA, err)
	}
	pr, ok := mergedAs(pulls, c.SHA)
	if !ok {
		return no("no merge group run has %s as its head and no merged pull request has it as its merge commit", c.SHA)
	}
	runs, err := src.Runs("pull_request", pr.HeadSHA)
	if err != nil {
		return no("the pipeline runs of pull request #%d could not be read: %v", pr.Number, err)
	}
	run, ok := newestRun(runs, "pull_request", pr.HeadSHA, c)
	if !ok {
		return no("no pipeline run of pull request #%d head %s on this repository's %s", pr.Number, pr.HeadSHA, c.Workflow)
	}
	return judge(src, c, reqs, run, fmt.Sprintf("pull request #%d", pr.Number))
}

// queueRefRe is the head ref the merge queue gives a group:
// gh-readonly-queue/<base branch>/pr-<number>-<the group's base sha>.
var queueRefRe = regexp.MustCompile(`^(?:refs/heads/)?gh-readonly-queue/.+/pr-([0-9]+)-([0-9a-f]+)$`)

// decideQueue answers whether a merge group's run may skip the suites because
// the one pull request in it already ran them green on this very tree, which
// is so when the pull request was based on the current trunk. A group of
// several pull requests builds a tree no single pull request run tested, so it
// is never reused; so is any group whose shape cannot be read.
func decideQueue(src Source, c Commit, reqs []Requirement) Verdict {
	if c.BaseSHA == "" || c.Parent == "" || c.Parent != c.BaseSHA {
		return no("the merge group may hold more than one pull request (its head's parent %q is not its base %q), and no single run tested its tree", c.Parent, c.BaseSHA)
	}
	m := queueRefRe.FindStringSubmatch(c.HeadRef)
	if m == nil {
		return no("the merge group head ref %q does not name one pull request", c.HeadRef)
	}
	number, err := strconv.Atoi(m[1])
	if err != nil || number < 1 {
		return no("the merge group head ref %q does not name one pull request", c.HeadRef)
	}
	if m[2] != c.BaseSHA {
		return no("the merge group head ref %q names base %s, not the group's base %s", c.HeadRef, m[2], c.BaseSHA)
	}
	// A pull request that moved after it was queued has a newer head whose run
	// tested another tree, which judge refuses.
	pr, err := src.Pull(number)
	if err != nil {
		return no("pull request #%d could not be read: %v", number, err)
	}
	runs, err := src.Runs("pull_request", pr.HeadSHA)
	if err != nil {
		return no("the pipeline runs of pull request #%d could not be read: %v", number, err)
	}
	run, ok := newestRun(runs, "pull_request", pr.HeadSHA, c)
	if !ok {
		return no("no pipeline run of pull request #%d head %s on this repository's %s", number, pr.HeadSHA, c.Workflow)
	}
	return judge(src, c, reqs, run, fmt.Sprintf("pull request #%d, the only one in the merge group,", number))
}

// judge holds one run to the rules every reused verdict meets: a first
// attempt, completed green, each required job green with its testing step run,
// and a recorded tested tree equal to the pushed commit's. who names whose run
// it is, for the reason.
func judge(src Source, c Commit, reqs []Requirement, run Run, who string) Verdict {
	if run.Attempt != 1 {
		return no("run %s is attempt %d, and a re-run may have tested another merge", run.URL, run.Attempt)
	}
	if run.Status != "completed" || run.Conclusion != "success" {
		return no("run %s is %s/%q, not success", run.URL, run.Status, run.Conclusion)
	}
	jobs, err := src.Jobs(run.ID)
	if err != nil {
		return no("the jobs of run %s could not be read: %v", run.URL, err)
	}
	for _, r := range reqs {
		if why := unmet(jobs, r); why != "" {
			return no("run %s: %s", run.URL, why)
		}
	}
	tree, err := src.TestedTree(run.ID)
	if err != nil {
		return no("the tree run %s tested could not be read: %v", run.URL, err)
	}
	if tree != c.Tree {
		return no("run %s tested tree %q, which differs from this commit's %s", run.URL, tree, c.Tree)
	}
	return Verdict{Reuse: true, Reason: fmt.Sprintf("reusing the verdict of %s, run %s, which tested tree %s", who, run.URL, c.Tree)}
}

// mergedAs is the merged pull request whose merge commit is sha.
func mergedAs(pulls []Pull, sha string) (Pull, bool) {
	for _, p := range pulls {
		if p.MergedAt != "" && p.MergeCommitSHA == sha {
			return p, true
		}
	}
	return Pull{}, false
}

// newestRun is the latest run of the event, of the workflow file, on headSHA,
// from this repository itself: a fork's run executes the fork's own workflow
// file.
func newestRun(runs []Run, event, headSHA string, c Commit) (Run, bool) {
	var best Run
	found := false
	for _, r := range runs {
		path, _, _ := strings.Cut(r.Path, "@")
		if r.Event != event || path != c.Workflow || r.HeadSHA != headSHA || r.HeadRepo != c.Repo {
			continue
		}
		if !found || r.Number > best.Number {
			best, found = r, true
		}
	}
	return best, found
}

// unmet says why the jobs do not meet r, or "" when they do.
func unmet(jobs []Job, r Requirement) string {
	seen := false
	for _, j := range jobs {
		if j.Name != r.Job && !strings.HasPrefix(j.Name, r.Job+" (") {
			continue
		}
		seen = true
		if j.Conclusion != "success" {
			return fmt.Sprintf("job `%s` concluded %q", j.Name, j.Conclusion)
		}
		if !ranStep(j, r.StepPrefix) {
			return fmt.Sprintf("job `%s` did not run its %q step to success", j.Name, r.StepPrefix)
		}
	}
	if !seen {
		return fmt.Sprintf("it has no `%s` job", r.Job)
	}
	return ""
}

func ranStep(j Job, prefix string) bool {
	for _, s := range j.Steps {
		if strings.HasPrefix(s.Name, prefix) && s.Conclusion == "success" {
			return true
		}
	}
	return false
}
