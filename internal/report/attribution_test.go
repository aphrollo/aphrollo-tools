package report

import (
	"errors"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// The tracker's escape issues, the head branch of a PR and the failed runs on main are
// read through the host and handed to the attribution, nothing else.
func TestGatherAttribution_ReadsIssuesPRBranchesAndRedRunsOnMain(t *testing.T) {
	failedAt := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	f := &host.Fake{
		ListIssuesFn: func(q host.IssueQuery) ([]host.Issue, error) {
			return []host.Issue{{Number: 481, Title: "escape: login 500s", Body: "PR #90"}}, nil
		},
		PRFn: func(n int) (*host.PR, error) {
			if n == 90 {
				return &host.PR{Number: 90, HeadRef: "lane/z"}, nil
			}
			return nil, nil
		},
		RunsOnFn: func(branch, event string, limit int) ([]host.WorkflowRun, error) {
			if branch != "main" || event != "push" {
				t.Errorf("RunsOn(%q, %q), want main and push", branch, event)
			}
			return []host.WorkflowRun{
				{Conclusion: "failure", CreatedAt: failedAt, URL: "u1", HeadSHA: "abc1234", Title: "Add b (#7)"},
				{Conclusion: "success", CreatedAt: failedAt.Add(time.Hour), URL: "u2"},
				{Conclusion: "cancelled", CreatedAt: failedAt.Add(2 * time.Hour), URL: "u3"},
			}, nil
		},
	}
	in, notes := GatherAttribution(f, "main")
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	if len(in.Issues) != 1 || in.Issues[0].Number != 481 || in.Issues[0].Body != "PR #90" {
		t.Errorf("issues = %+v", in.Issues)
	}
	if b, ok := in.PRBranch(90); !ok || b != "lane/z" {
		t.Errorf("PRBranch(90) = %q, %v; want lane/z", b, ok)
	}
	if _, ok := in.PRBranch(91); ok {
		t.Errorf("PRBranch(91) found a branch for a PR the host does not know")
	}
	if len(in.MainReds) != 1 || !in.MainReds[0].At.Equal(failedAt) || in.MainReds[0].SHA != "abc1234" || in.MainReds[0].Title != "Add b (#7)" {
		t.Errorf("main reds = %+v, want the one failed run (a success and a cancelled run are not red)", in.MainReds)
	}
}

// A host that cannot be read is said so, never silently treated as "no escapes".
func TestGatherAttribution_AnUnreadableHostIsNamedInTheNotes(t *testing.T) {
	boom := errors.New("gh: network timeout")
	f := &host.Fake{
		ListIssuesFn: func(host.IssueQuery) ([]host.Issue, error) { return nil, boom },
		RunsOnFn:     func(string, string, int) ([]host.WorkflowRun, error) { return nil, boom },
	}
	_, notes := GatherAttribution(f, "main")
	if len(notes) != 2 {
		t.Fatalf("notes = %v, want one for the issues and one for the runs", notes)
	}
}
