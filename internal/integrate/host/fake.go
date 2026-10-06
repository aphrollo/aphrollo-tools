package host

import (
	"sync"
	"time"
)

// Fake is a Host a test drives: each method answers from the function of its
// name, or with the zero answer when the test gave none, and every call is
// recorded in Calls. It is safe for concurrent use.
type Fake struct {
	FindPRFn          func(branch string) (int, bool, error)
	PRFn              func(number int) (*PR, error)
	PRByBranchFn      func(branch string) (*PR, error)
	PRByRefFn         func(ref string) (*PR, error)
	PRTextFn          func(branch string) (string, string, error)
	OpenPRFn          func(OpenRequest) (*PR, error)
	EditBodyFn        func(branch, body string) error
	MarkReadyFn       func(branch string) error
	SummaryFn         func(number int) (*Summary, error)
	ChecksAtFn        func(sha string) ([]Check, error)
	RerunFailedJobsFn func(run int64) error
	RunInfoFn         func(id int64) (RunInfo, error)
	PRRunsFn          func(branch string, pr int) ([]PRRun, error)
	RunFailedJobsFn   func(id int64, attempt int) ([]string, error)
	RunFirstAttemptFn func(id int64) (string, string, error)
	HasMergeQueueFn   func(repo, base string) (bool, error)
	EnqueueFn         func(repo string, pr int, sha string) error
	MergeFn           func(MergeRequest) error
	QueueEntryFn      func(repo string, pr int) (*QueueEntry, error)
	DequeueFn         func(entryID string) error
	QueueRemovalFn    func(repo string, pr int) (QueueRemoval, error)
	MergeGroupRunFn   func(repo string, pr int) (int64, error)
	DequeueHintText   string
	ResolveRunFn      func(RunTarget) (int64, error)
	RunFn             func(id int64) (*WorkflowRun, error)
	RunsOnFn          func(branch, event string, limit int) ([]WorkflowRun, error)
	JobAnnotationsFn  func(job int64) ([]string, error)
	JobLogFn          func(job int64) ([]byte, error)
	RunLogFn          func(run int64) ([]byte, error)
	OpenIssueFn       func(IssueRequest) (string, error)
	CloseIssueFn      func(number int, comment string) error
	WhoamiFn          func() (string, error)
	EnsureLabelFn     func(name, colour, description string) error
	ListIssuesFn      func(IssueQuery) ([]Issue, error)
	IssueFn           func(number string) (*Issue, error)
	PRBodyFn          func(ref string) (string, error)
	PRClosureFn       func(ref string) (*PRFacts, error)
	PRDiffFn          func(ref string) (string, error)
	CheckStateFn      func(sha, name string) (string, string, bool, error)
	ArtifactRunFn     func(name string) (int64, bool, error)
	DownloadFn        func(run int64, name, dir string) error
	ProbeValue        Probe

	mu    sync.Mutex
	calls []string
}

var _ Host = (*Fake)(nil)

// Calls is the name of each method called, in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *Fake) note(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *Fake) FindPR(branch string) (int, bool, error) {
	f.note("FindPR")
	if f.FindPRFn == nil {
		return 0, false, nil
	}
	return f.FindPRFn(branch)
}

func (f *Fake) PR(number int) (*PR, error) {
	f.note("PR")
	if f.PRFn == nil {
		return nil, nil
	}
	return f.PRFn(number)
}

func (f *Fake) PRByBranch(branch string) (*PR, error) {
	f.note("PRByBranch")
	if f.PRByBranchFn == nil {
		return nil, nil
	}
	return f.PRByBranchFn(branch)
}

func (f *Fake) PRByRef(ref string) (*PR, error) {
	f.note("PRByRef")
	if f.PRByRefFn == nil {
		return nil, nil
	}
	return f.PRByRefFn(ref)
}

func (f *Fake) PRText(branch string) (string, string, error) {
	f.note("PRText")
	if f.PRTextFn == nil {
		return "", "", nil
	}
	return f.PRTextFn(branch)
}

func (f *Fake) OpenPR(r OpenRequest) (*PR, error) {
	f.note("OpenPR")
	if f.OpenPRFn == nil {
		return nil, nil
	}
	return f.OpenPRFn(r)
}

func (f *Fake) EditBody(branch, body string) error {
	f.note("EditBody")
	if f.EditBodyFn == nil {
		return nil
	}
	return f.EditBodyFn(branch, body)
}

func (f *Fake) MarkReady(branch string) error {
	f.note("MarkReady")
	if f.MarkReadyFn == nil {
		return nil
	}
	return f.MarkReadyFn(branch)
}

func (f *Fake) Summary(number int) (*Summary, error) {
	f.note("Summary")
	if f.SummaryFn == nil {
		return nil, nil
	}
	return f.SummaryFn(number)
}

func (f *Fake) ChecksAt(sha string) ([]Check, error) {
	f.note("ChecksAt")
	if f.ChecksAtFn == nil {
		return nil, nil
	}
	return f.ChecksAtFn(sha)
}

func (f *Fake) RerunFailedJobs(run int64) error {
	f.note("RerunFailedJobs")
	if f.RerunFailedJobsFn == nil {
		return nil
	}
	return f.RerunFailedJobsFn(run)
}

func (f *Fake) RunInfo(id int64) (RunInfo, error) {
	f.note("RunInfo")
	if f.RunInfoFn == nil {
		return RunInfo{}, nil
	}
	return f.RunInfoFn(id)
}

func (f *Fake) PRRuns(branch string, pr int) ([]PRRun, error) {
	f.note("PRRuns")
	if f.PRRunsFn == nil {
		return nil, nil
	}
	return f.PRRunsFn(branch, pr)
}

func (f *Fake) RunFailedJobs(id int64, attempt int) ([]string, error) {
	f.note("RunFailedJobs")
	if f.RunFailedJobsFn == nil {
		return nil, nil
	}
	return f.RunFailedJobsFn(id, attempt)
}

func (f *Fake) RunFirstAttempt(id int64) (string, string, error) {
	f.note("RunFirstAttempt")
	if f.RunFirstAttemptFn == nil {
		return "", "", nil
	}
	return f.RunFirstAttemptFn(id)
}

func (f *Fake) HasMergeQueue(repo, base string) (bool, error) {
	f.note("HasMergeQueue")
	if f.HasMergeQueueFn == nil {
		return false, nil
	}
	return f.HasMergeQueueFn(repo, base)
}

func (f *Fake) Enqueue(repo string, pr int, sha string) error {
	f.note("Enqueue")
	if f.EnqueueFn == nil {
		return nil
	}
	return f.EnqueueFn(repo, pr, sha)
}

func (f *Fake) Merge(r MergeRequest) error {
	f.note("Merge")
	if f.MergeFn == nil {
		return nil
	}
	return f.MergeFn(r)
}

func (f *Fake) DequeueHint(pr int) string { return f.DequeueHintText }

func (f *Fake) QueueEntry(repo string, pr int) (*QueueEntry, error) {
	f.note("QueueEntry")
	if f.QueueEntryFn == nil {
		return nil, nil
	}
	return f.QueueEntryFn(repo, pr)
}

func (f *Fake) Dequeue(entryID string) error {
	f.note("Dequeue")
	if f.DequeueFn == nil {
		return nil
	}
	return f.DequeueFn(entryID)
}

func (f *Fake) QueueRemoval(repo string, pr int) (QueueRemoval, error) {
	f.note("QueueRemoval")
	if f.QueueRemovalFn == nil {
		return QueueRemoval{}, nil
	}
	return f.QueueRemovalFn(repo, pr)
}

func (f *Fake) MergeGroupRun(repo string, pr int) (int64, error) {
	f.note("MergeGroupRun")
	if f.MergeGroupRunFn == nil {
		return 0, nil
	}
	return f.MergeGroupRunFn(repo, pr)
}

func (f *Fake) ResolveRun(t RunTarget) (int64, error) {
	f.note("ResolveRun")
	if f.ResolveRunFn == nil {
		return t.Run, nil
	}
	return f.ResolveRunFn(t)
}

func (f *Fake) Run(id int64) (*WorkflowRun, error) {
	f.note("Run")
	if f.RunFn == nil {
		return nil, nil
	}
	return f.RunFn(id)
}

func (f *Fake) RunsOn(branch, event string, limit int) ([]WorkflowRun, error) {
	f.note("RunsOn")
	if f.RunsOnFn == nil {
		return nil, nil
	}
	return f.RunsOnFn(branch, event, limit)
}

func (f *Fake) JobAnnotations(job int64) ([]string, error) {
	f.note("JobAnnotations")
	if f.JobAnnotationsFn == nil {
		return nil, nil
	}
	return f.JobAnnotationsFn(job)
}

func (f *Fake) JobLog(job int64) ([]byte, error) {
	f.note("JobLog")
	if f.JobLogFn == nil {
		return nil, nil
	}
	return f.JobLogFn(job)
}

func (f *Fake) RunLog(run int64) ([]byte, error) {
	f.note("RunLog")
	if f.RunLogFn == nil {
		return nil, nil
	}
	return f.RunLogFn(run)
}

func (f *Fake) OpenIssue(r IssueRequest) (string, error) {
	f.note("OpenIssue")
	if f.OpenIssueFn == nil {
		return "", nil
	}
	return f.OpenIssueFn(r)
}

func (f *Fake) EnsureLabel(name, colour, description string) error {
	f.note("EnsureLabel")
	if f.EnsureLabelFn == nil {
		return nil
	}
	return f.EnsureLabelFn(name, colour, description)
}

func (f *Fake) ListIssues(q IssueQuery) ([]Issue, error) {
	f.note("ListIssues")
	if f.ListIssuesFn == nil {
		return nil, nil
	}
	return f.ListIssuesFn(q)
}

func (f *Fake) Issue(number string) (*Issue, error) {
	f.note("Issue")
	if f.IssueFn == nil {
		return nil, nil
	}
	return f.IssueFn(number)
}

func (f *Fake) PRBody(ref string) (string, error) {
	f.note("PRBody")
	if f.PRBodyFn == nil {
		return "", nil
	}
	return f.PRBodyFn(ref)
}

func (f *Fake) PRClosure(ref string) (*PRFacts, error) {
	f.note("PRClosure")
	if f.PRClosureFn == nil {
		return nil, nil
	}
	return f.PRClosureFn(ref)
}

func (f *Fake) PRDiff(ref string) (string, error) {
	f.note("PRDiff")
	if f.PRDiffFn == nil {
		return "", nil
	}
	return f.PRDiffFn(ref)
}

func (f *Fake) CheckState(sha, name string) (string, string, bool, error) {
	f.note("CheckState")
	if f.CheckStateFn == nil {
		return "", "", false, nil
	}
	return f.CheckStateFn(sha, name)
}

func (f *Fake) ArtifactRun(name string) (int64, bool, error) {
	f.note("ArtifactRun")
	if f.ArtifactRunFn == nil {
		return 0, false, nil
	}
	return f.ArtifactRunFn(name)
}

func (f *Fake) DownloadArtifact(run int64, name, dir string) error {
	f.note("DownloadArtifact")
	if f.DownloadFn == nil {
		return nil
	}
	return f.DownloadFn(run, name, dir)
}

func (f *Fake) Probe(bool) Probe { return f.ProbeValue }

// Within answers the same fake: a fake has no clock to bound.
func (f *Fake) Within(time.Duration) Host { return f }

var _ IssueCloser = (*Fake)(nil)

func (f *Fake) CloseIssue(number int, comment string) error {
	f.note("CloseIssue")
	if f.CloseIssueFn == nil {
		return nil
	}
	return f.CloseIssueFn(number, comment)
}

var _ Identity = (*Fake)(nil)

func (f *Fake) Whoami() (string, error) {
	f.note("Whoami")
	if f.WhoamiFn == nil {
		return "", nil
	}
	return f.WhoamiFn()
}
