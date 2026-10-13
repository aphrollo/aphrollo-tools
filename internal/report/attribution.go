package report

import (
	"fmt"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// attributionIssueLimit and attributionRunLimit bound the reads; a tracker with more is
// read newest first.
const (
	attributionIssueLimit = 300
	attributionRunLimit   = 100
)

// GatherAttribution reads, through the host, what the escape attribution needs beyond the
// event log: the tracker's issues, a PR's head branch, and the failed runs on the trunk.
// A merge gate that blocked is not among them: it refused an incoming merge.
// What it cannot read is named in the notes and the attribution goes on without it.
func GatherAttribution(h host.Host, trunk string) (*measure.HostFacts, []string) {
	in := &measure.HostFacts{}
	var notes []string
	issues, err := h.ListIssues(host.IssueQuery{State: "all", Limit: attributionIssueLimit, Fields: []string{"number", "title", "body"}})
	if err != nil {
		notes = append(notes, fmt.Sprintf("escape issues not read: %v", err))
	}
	for _, is := range issues {
		in.Issues = append(in.Issues, measure.EscapeIssue{Number: is.Number, Title: is.Title, Body: is.Body})
	}
	branches := map[int]string{}
	in.PRBranch = func(n int) (string, bool) {
		if b, ok := branches[n]; ok {
			return b, b != ""
		}
		pr, err := h.PR(n)
		if err != nil || pr == nil {
			branches[n] = ""
			return "", false
		}
		branches[n] = pr.HeadRef
		return pr.HeadRef, pr.HeadRef != ""
	}
	runs, err := h.RunsOn(trunk, "push", attributionRunLimit)
	if err != nil {
		notes = append(notes, fmt.Sprintf("runs on %s not read: %v", trunk, err))
	}
	for _, r := range runs {
		if r.Conclusion == "failure" {
			in.MainReds = append(in.MainReds, measure.RedRun{At: r.CreatedAt, SHA: r.HeadSHA, Title: r.Title, Ref: r.URL})
		}
	}
	return in, notes
}

// laneCost is the agent cost of each lane in USD, from the usage section; nil without one.
func laneCost(u *Usage) map[string]float64 {
	if u == nil {
		return nil
	}
	out := make(map[string]float64, len(u.ByLane))
	for _, g := range u.ByLane {
		out[g.Key] = g.CostUSD
	}
	return out
}
