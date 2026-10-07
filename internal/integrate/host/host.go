// Package host is the port to the code host: every call the pipeline makes to
// the place a repository's pull requests, checks, merge queue and issues live
// goes through the Host interface here, and no other package speaks to one.
//
// The GitHub adapter (package github) drives gh and GitHub's REST and GraphQL
// APIs. A second adapter, the local one the ci = local landing needs, is one
// more type that implements Land and what Land is built from; the seam for it
// is the Land method and the Landing capability, which an adapter with no merge
// queue answers with false. Another host is one more adapter. A test uses
// Fake, which answers from functions it is given and records each call.
package host

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Host is the whole port. A caller that needs a slice of it takes the smaller
// interface below.
type Host interface {
	PullRequests
	Checks
	Landing
	Runs
	Issues
	Reach
	// Within is the same host with every call bounded by d: a collector that
	// has a budget for its whole batch gives each call what is left of it.
	Within(d time.Duration) Host
}

// PullRequests reads and writes a branch's pull request.
type PullRequests interface {
	// FindPR is the number of the newest PR for branch, any state; found is
	// false when the branch has none.
	FindPR(branch string) (number int, found bool, err error)
	// PR is the PR with this number. It is the one read that carries mergeable
	// and the head commit.
	PR(number int) (*PR, error)
	// PRByBranch is the newest PR for branch; nil, nil when there is none.
	PRByBranch(branch string) (*PR, error)
	// PRByRef is PRByBranch or PR, by whether ref is a number.
	PRByRef(ref string) (*PR, error)
	// PRText is the title and body the PR for branch holds now.
	PRText(branch string) (title, body string, err error)
	// OpenPR opens a PR. Title and body are what the caller wants on it.
	OpenPR(OpenRequest) (*PR, error)
	// EditBody sets the body of the PR for branch.
	EditBody(branch, body string) error
	// MarkReady takes the PR for branch out of draft.
	MarkReady(branch string) error
	// Summary is the PR with this number as the retrospective reads it.
	Summary(number int) (*Summary, error)
	// PRBody is the body of the PR ref names (a number, URL or branch).
	PRBody(ref string) (string, error)
	// PRClosure is what the PR ref names says it closes and the two ends of its
	// diff: its body, every commit message in it, and the base and head commits.
	PRClosure(ref string) (*PRFacts, error)
	// PRDiff is the full patch of the PR ref names. An error carries the host's
	// own words, so a caller can tell a diff too large to serve.
	PRDiff(ref string) (string, error)
}

// Checks reads what CI said about a commit.
type Checks interface {
	// ChecksAt is every check run and commit status on one commit, by SHA, so a
	// result can never belong to a different head than the one asked about.
	ChecksAt(sha string) ([]Check, error)
	// RunInfo is the workflow file and attempt of one Actions run.
	RunInfo(id int64) (RunInfo, error)
	// PRRuns is every pull_request run that belongs to the PR, whatever the
	// branch's other history holds.
	PRRuns(branch string, pr int) ([]PRRun, error)
	// RunFailedJobs names the jobs of a run's attempt that failed.
	RunFailedJobs(id int64, attempt int) ([]string, error)
	// RunFirstAttempt is how the first attempt of a run ended.
	RunFirstAttempt(id int64) (status, conclusion string, err error)
	// RerunFailedJobs asks the host to run again the failed, cancelled and
	// timed-out jobs of one Actions run.
	RerunFailedJobs(run int64) error
	// CheckState is the newest check run named name on the commit sha; found is
	// false when the commit has none.
	CheckState(sha, name string) (status, conclusion string, found bool, err error)
}

// Landing puts a PR on its base branch and reads where a queued PR stands.
type Landing interface {
	// HasMergeQueue is whether base of repo (owner/name, "" for origin) has a
	// merge queue.
	HasMergeQueue(repo, base string) (bool, error)
	// Enqueue puts the PR in the base branch's merge queue, bound to sha: the
	// host itself refuses it (a *HeadMovedError) when the head is anything else.
	Enqueue(repo string, pr int, sha string) error
	// Merge merges the PR for the branch, bound to Head the same way.
	Merge(MergeRequest) error
	// DequeueHint says how to take a PR out of the queue by hand.
	DequeueHint(pr int) string
	// QueueEntry is where a PR stands in its merge queue; nil, nil when it is
	// in none.
	QueueEntry(repo string, pr int) (*QueueEntry, error)
	// Dequeue takes a queue entry back out of the queue.
	Dequeue(entryID string) error
	// QueueRemoval is what the PR's timeline says about leaving the queue.
	QueueRemoval(repo string, pr int) (QueueRemoval, error)
	// MergeGroupRun is the newest merge_group run the queue made for a PR; 0
	// when there is none.
	MergeGroupRun(repo string, pr int) (int64, error)
}

// Runs reads workflow runs, to say why one is red.
type Runs interface {
	// ResolveRun turns a target into a run id.
	ResolveRun(RunTarget) (int64, error)
	// Run is one run with its jobs and their steps.
	Run(id int64) (*WorkflowRun, error)
	// RunsOn is the pull_request runs of a branch, newest first.
	RunsOn(branch, event string, limit int) ([]WorkflowRun, error)
	// JobAnnotations are the annotation messages of one job's check run.
	JobAnnotations(job int64) ([]string, error)
	// JobLog is the failed-step log of one job, ErrLogMissing when the host no
	// longer has it.
	JobLog(job int64) ([]byte, error)
	// RunLog is the failed-step log of a whole run.
	RunLog(run int64) ([]byte, error)
	// ArtifactRun is the id of the run that published the newest artifact called
	// name; found is false when none has.
	ArtifactRun(name string) (run int64, found bool, err error)
	// DownloadArtifact puts the artifact called name that run published into dir.
	DownloadArtifact(run int64, name, dir string) error
}

// Issues opens issues in a repository's tracker.
type Issues interface {
	// OpenIssue opens one issue and answers its URL. Repo "" is the
	// repository the host was made for.
	OpenIssue(IssueRequest) (url string, err error)
	// EnsureLabel creates a label that may not exist yet; it is idempotent.
	EnsureLabel(name, colour, description string) error
	// ListIssues is the issues of the host's own repository that match q, newest
	// first, carrying the fields q asks for.
	ListIssues(IssueQuery) ([]Issue, error)
	// Issue is one issue, by number, with its labels and body.
	Issue(number string) (*Issue, error)
}

// Reach says what this box can reach of the host.
type Reach interface {
	// Probe is the probe; withGraphQL also tries GraphQL, which a verb that only
	// reads over REST has no reason to wait for.
	Probe(withGraphQL bool) Probe
}

// PR is a pull request as the verbs read it.
type PR struct {
	Number int
	URL    string
	State  string // OPEN | MERGED | CLOSED
	// IsDraft is whether the PR is a draft.
	IsDraft bool
	// Mergeable is the host's async-computed merge verdict: MERGEABLE |
	// CONFLICTING | UNKNOWN (for a short window right after a push).
	Mergeable string
	// MergeStateStatus is the finer merge state: DIRTY | BEHIND | BLOCKED |
	// CLEAN | UNSTABLE | DRAFT | UNKNOWN.
	MergeStateStatus string
	// HeadSHA is the commit the host holds as the PR's head, HeadRef its branch.
	HeadSHA string
	HeadRef string
	// BaseRef is the branch the PR merges into, BaseRepo the repository it
	// merges into ("owner/name"); empty when the host did not say.
	BaseRef  string
	BaseRepo string
	// MergedAt is when the PR merged, RFC 3339; empty while it has not.
	MergedAt string
}

// Check is one check (or legacy commit status) on one commit. SHA is the commit
// it ran on: a check whose SHA is not the PR's current head is a leftover from
// an older push.
type Check struct {
	ID         int64  `json:"id"`
	App        string `json:"app"`
	NotStarted bool   `json:"-"`
	// NotAcquired is a NotStarted job GitHub cancelled because no hosted runner
	// took it: asking for it again can work, where a billing lock never does.
	NotAcquired bool   `json:"-"`
	Name        string `json:"name"`
	SHA         string `json:"head_sha"`
	Status      string `json:"status"`     // queued | in_progress | completed
	Conclusion  string `json:"conclusion"` // success | failure | ... once completed
	StartedAt   string `json:"started_at"` // RFC 3339; empty for a commit status
	URL         string `json:"html_url"`
}

// RunInfo is what the Actions run behind a check says about itself.
type RunInfo struct {
	Workflow string // the workflow file's base name, such as pipeline.yml
	Attempt  int
	Event    string // the event that made the run, such as pull_request or push
}

// PRRun is one workflow run of a pull_request event.
type PRRun struct {
	ID         int64  `json:"databaseId"`
	SHA        string `json:"headSha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	CreatedAt  string `json:"createdAt"`
	Attempt    int    `json:"attempt"`
}

// OpenRequest is the PR to open.
type OpenRequest struct {
	Base   string
	Branch string
	Title  string
	Body   string
	Draft  bool
}

// Summary is a PR as the retrospective reads it.
type Summary struct {
	Number    int       `json:"number"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	MergedAt  time.Time `json:"mergedAt"`
	HeadRef   string    `json:"headRefName"`
}

// LandRequest is the PR to land. Head is the commit the PR was judged at, and
// the one thing the landing is bound to.
type LandRequest struct {
	Branch string
	PR     int
	URL    string
	// Repo is the repository the PR merges into ("owner/name"); "" is the
	// host's own. Base is the branch it merges into.
	Repo, Base string
	Head       string
	// Method is squash | merge | rebase; a merge queue sets its own and ignores it.
	Method string
	// Subject and Body are the commit message when UseBody is set; otherwise the
	// host writes one from the PR's title.
	Subject, Body string
	UseBody       bool
}

// MergeRequest is the merge of one PR, bound to Head.
type MergeRequest struct {
	Branch, Method, Head string
	Subject, Body        string
	UseBody              bool
}

// Landed is what Land did. Queued is true when the PR is in a merge queue and
// has not merged. Already is true when it was in the queue before this call.
// Entry is where it stands, nil when the queue has not said yet.
type Landed struct {
	Queued  bool
	Already bool
	Entry   *QueueEntry
}

// QueueEntry is where a queued PR stands: its place in the queue (1 is next to
// merge), how many PRs the queue holds, the host's state word for the entry
// (QUEUED, AWAITING_CHECKS, MERGEABLE, UNMERGEABLE, LOCKED) and its id.
type QueueEntry struct {
	ID       string
	Position int
	Total    int
	State    string
}

func (e *QueueEntry) String() string {
	place := "position " + strconv.Itoa(e.Position)
	if e.Total > 0 {
		place += " of " + strconv.Itoa(e.Total)
	}
	if e.State == "" {
		return place
	}
	return place + " (" + strings.ToLower(strings.ReplaceAll(e.State, "_", " ")) + ")"
}

// QueueRemoval is what the PR's timeline says about leaving the merge queue:
// whether its latest queue event is a removal (and the host's reason, such as
// failed_checks), and whether auto-merge is still switched on for it.
type QueueRemoval struct {
	Removed bool
	// FailedChecks is whether ANY removal on the timeline had the reason
	// failed_checks, even one the PR was queued again after.
	FailedChecks bool
	Reason       string
	AutoMerge    bool
}

// RunTarget names the run to explain. Run wins over Main, Main over PR; with
// none of them set the PR is the current branch's.
type RunTarget struct {
	Run      int64
	PR       int
	Main     bool
	Workflow string
}

// WorkflowRun is one run with its jobs.
type WorkflowRun struct {
	Attempt    int       `json:"attempt"`
	Conclusion string    `json:"conclusion"`
	ID         int64     `json:"databaseId"`
	Event      string    `json:"event"`
	HeadBranch string    `json:"headBranch"`
	HeadSHA    string    `json:"headSha"`
	Status     string    `json:"status"`
	URL        string    `json:"url"`
	Workflow   string    `json:"workflowName"`
	CreatedAt  time.Time `json:"createdAt"`
	Jobs       []Job     `json:"jobs"`
}

// Job is one job of a run.
type Job struct {
	ID         int64  `json:"databaseId"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	Steps      []Step `json:"steps"`
}

// Step is one step of a job.
type Step struct {
	Name       string `json:"name"`
	Number     int    `json:"number"`
	Conclusion string `json:"conclusion"`
}

// IssueRequest is the issue to open. Repo is "owner/name" of another tracker,
// "" for the host's own.
type IssueRequest struct {
	Title  string
	Body   string
	Repo   string
	Labels []string
}

// IssueQuery says which issues to list. State is open, closed or all. A Label
// is one label (several would be read as all-of, so a caller asks once per
// label). Limit is the page size, gh's own when zero. Fields name what each issue carries, spelled
// as GitHub does: number, title, body, state, closedAt, labels; a field not asked
// for is left zero, and a listing of everything is not asked for by accident.
type IssueQuery struct {
	Label  string
	State  string
	Limit  int
	Fields []string
}

// Issue is an issue as a listing carries it.
type Issue struct {
	Number   int
	Title    string
	Body     string
	State    string
	ClosedAt time.Time // zero when the issue is open or the host gave no date
	// Author is the login that opened the issue, when the query asked for it.
	Author string
	Labels []string
}

// PRFacts is what a PR says it closes and the ends of its diff.
type PRFacts struct {
	Body    string
	Commits []CommitText
	Base    string
	Head    string
}

// CommitText is the message of one commit in a PR.
type CommitText struct {
	Headline string
	Body     string
}

// Probe is what this box can reach of the host right now.
type Probe struct {
	Present   bool   // the host's client is there at all
	RESTOK    bool   // authenticated for the REST calls the verbs make
	GraphQLOK bool   // GraphQL is reachable too
	Detail    string // the client's own complaint from the first probe that failed
}

// Ready reports whether the box can drive the verbs: GraphQL is not required.
func (p Probe) Ready() bool { return p.Present && p.RESTOK }

// ErrLogMissing is a job log the host no longer holds.
var ErrLogMissing = errors.New("job log not found")

// HeadMovedError is the host refusing a landing because the PR's head is no
// longer the commit the landing was bound to, or Land finding that it moved
// after it enqueued the PR. Msg is the whole line the operator reads.
type HeadMovedError struct{ Msg string }

func (e *HeadMovedError) Error() string { return e.Msg }

// HeadMoved is the error a merge bound to sha is refused with.
func HeadMoved(sha string) error {
	return &HeadMovedError{Msg: fmt.Sprintf("PR head moved after it was judged (merge bound to %s) — merge again to judge the new head", Short(sha))}
}

// Short is the first seven characters of a commit id.
func Short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// NotAHostRemote is the error for a repository whose origin the host does not
// serve.
func NotAHostRemote(dir string) error {
	return fmt.Errorf("origin is not a github remote in %s", dir)
}

// MergeSubject is the subject every merge this pipeline makes carries: the PR's
// title and number, never the host's default line naming the branch.
func MergeSubject(title string, n int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Sprintf("Pull request #%d", n)
	}
	suffix := fmt.Sprintf("(#%d)", n)
	if strings.HasSuffix(title, suffix) {
		return title
	}
	return title + " " + suffix
}

// FixLine names the one thing to do next, "" when Ready().
func (p Probe) FixLine() string {
	switch {
	case !p.Present:
		return "gh not found on PATH — install gh (https://cli.github.com), then `gh auth login` or set GH_TOKEN"
	case !p.RESTOK:
		return "gh is not authenticated — run `gh auth login` or set GH_TOKEN"
	default:
		return ""
	}
}

// TransportLine describes the resolved state for a doctor or status report.
func (p Probe) TransportLine() string {
	switch {
	case !p.Ready():
		return p.FixLine()
	case p.GraphQLOK:
		return "REST and GraphQL both available"
	default:
		return "REST available, GraphQL unavailable (blocked or unauthenticated) — pr view/create/merge use the REST fallback"
	}
}

// LandHost is what Land needs of a host: the Landing primitives and the read
// of a PR by branch, to see that its head did not move after the enqueue.
type LandHost interface {
	Landing
	PRByBranch(branch string) (*PR, error)
}

// RunID is the Actions run the check belongs to, read from its URL; 0 when the
// check is not an Actions job (a commit status, another app).
func RunID(c Check) int64 {
	_, rest, ok := strings.Cut(c.URL, "/actions/runs/")
	if !ok {
		return 0
	}
	digits := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		digits = rest[:i]
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// IssueCloser closes an issue with a comment. It is apart from Issues so a
// caller that only opens issues is not made to implement it.
type IssueCloser interface {
	// CloseIssue comments on the issue and closes it; comment may be empty.
	CloseIssue(number int, comment string) error
}

// Identity says which account the host acts as.
type Identity interface {
	// Whoami is the login the host's calls are made as.
	Whoami() (string, error)
}
