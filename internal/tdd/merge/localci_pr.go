package merge

import (
	"fmt"
	"io"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
)

// ciPullRequest is the PR a local CI run judges, as a workflow reads it from
// github.event.pull_request.
type ciPullRequest struct {
	Number int
	URL    string
	Title  string
}

// ciPullRequestTimeout bounds the host reads that name the PR: the run does not
// wait on a host that is down, which is the very case local CI exists for.
const ciPullRequestTimeout = 20 * time.Second

// ciPullRequestOf reads the PR of the lane's branch from the host. A variable
// so a test names the PR without a host.
var ciPullRequestOf = func(lane string) (ciPullRequest, error) {
	branch := laneBranchOf(lane)
	if branch == "" {
		return ciPullRequest{}, fmt.Errorf("the lane is on no branch")
	}
	h := github.New(github.Options{Dir: lane, Origin: func() string { return originURL(lane) }, Timeout: ciPullRequestTimeout})
	pr, err := h.PRByBranch(branch)
	if err != nil {
		return ciPullRequest{}, err
	}
	if pr == nil {
		return ciPullRequest{}, fmt.Errorf("branch %s has no pull request", branch)
	}
	title, _, err := h.PRText(branch)
	if err != nil {
		return ciPullRequest{}, err
	}
	return ciPullRequest{Number: pr.Number, URL: pr.URL, Title: title}, nil
}

// addPullRequest puts the PR's fields in the event. A PR that cannot be read
// is said on the log and left out: a workflow then reads empty fields, as it
// would for an event with none, and the run still judges the tree.
func addPullRequest(event map[string]any, lane string, log io.Writer) {
	pr, err := ciPullRequestOf(lane)
	if err != nil {
		fmt.Fprintf(log, "ci local: [note] the pull request could not be read (%v): github.event.pull_request.number and its other fields stay empty\n", err)
		return
	}
	event["pr_number"], event["pr_url"], event["pr_title"] = pr.Number, pr.URL, pr.Title
}
