package workspace

import (
	"fmt"
	"strings"
)

// PRStatus is the subset of a GitHub PR that answers "was my worktree merged,
// and if not, can it be?" — kept small so the rendered line stays a single
// terse row instead of the multi-field JSON blob a raw `gh pr view` dumps.
type PRStatus struct {
	Number              int
	State               string // OPEN | MERGED | CLOSED
	IsDraft             bool   // OPEN PR still a draft (not yet ready for review)
	MergedAt            string // RFC3339 when State==MERGED, else ""
	Mergeable           string // MERGEABLE | CONFLICTING | UNKNOWN
	MergeStateStatus    string // CLEAN | BLOCKED | BEHIND | UNSTABLE | DIRTY | …
	Pass, Fail, Pending int    // status-check rollup, bucketed
}

// checkEntry is one node of gh's statusCheckRollup. CheckRun nodes carry
// status+conclusion; legacy StatusContext nodes carry state. classifyCheck
// reads whichever is populated.
type checkEntry struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// ghViewPRStatus is the seam over `gh pr view`, a package var (bound to
// ghViewPRStatusReal) so tests drive the rendering without gh or the
// network. It returns (nil, nil) when the branch has no PR — the "nothing
// to report" signal, not an error (mirrors ghViewPR).
var ghViewPRStatus = ghViewPRStatusReal

// ghViewPRStatusReal is ghViewPRStatus's real implementation, named so a
// test can call it directly regardless of what another test's stub last
// pointed the ghViewPRStatus var at.
//
// Routed over REST (ghAPIViewByBranch, #880), the same as ghViewPR: absence
// is REST's list-pulls endpoint answering an empty array, not a message to
// sniff off a non-zero exit, so a genuine failure — a network timeout, a
// missing gh, no auth — always propagates as an error rather than silently
// reporting the branch as unmerged and un-PR'd (the shape ghViewPR had until
// #348).
func ghViewPRStatusReal(wt, branch string) (*PRStatus, error) {
	p, err := ghAPIViewByBranch(wt, branch)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil // absence-ok: REST's list-pulls returned no entry for branch
	}
	s := &PRStatus{
		Number:           p.Number,
		State:            p.state(),
		IsDraft:          p.Draft,
		MergedAt:         p.MergedAt,
		Mergeable:        p.mergeableWord(),
		MergeStateStatus: p.mergeStateStatus(),
	}
	// REST's single-pull response carries no statusCheckRollup (a GraphQL-only
	// aggregate) — reuse ghChecksAt, the same commits/{sha}/check-runs +
	// .../status REST reader `merge --wait` already relies on, to rebuild the
	// pass/fail/pending tally from the PR's actual head commit.
	if p.state() == "OPEN" {
		runs, err := ghChecksAt(wt, p.Head.SHA)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			switch classifyCheckRun(r) {
			case "pass":
				s.Pass++
			case "fail":
				s.Fail++
			default:
				s.Pending++
			}
		}
	}
	return s, nil
}

// classifyCheckRun buckets one CheckRun (mergewait.go's REST check-run/status
// shape) into pass / fail / pending, mirroring classifyCheck's vocabulary for
// the GraphQL-era checkEntry shape above. Anything not yet "completed" is
// pending regardless of its conclusion field.
func classifyCheckRun(c CheckRun) string {
	if strings.ToLower(c.Status) != "completed" {
		return "pending"
	}
	switch strings.ToUpper(c.Conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return "pass"
	case "":
		return "pending"
	default:
		return "fail"
	}
}

// classifyCheck buckets one rollup node into pass / fail / pending. It prefers
// the CheckRun conclusion, falls back to the StatusContext state, and treats
// anything still running (or unrecognized) as pending so an in-flight CI run is
// never miscounted as a pass.
func classifyCheck(c checkEntry) string {
	s := strings.ToUpper(c.Conclusion)
	if s == "" {
		s = strings.ToUpper(c.State)
	}
	switch s {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return "pass"
	case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return "fail"
	default:
		return "pending"
	}
}

// Status resolves the target branch's PR and renders the one-line summary.
func Status(t *Target) (string, error) {
	s, err := ghViewPRStatus(t.Worktree, t.Branch)
	if err != nil {
		return "", err
	}
	return s.Line(t.Branch), nil
}

// Line renders the terse status row. A nil receiver (no PR) is valid and
// reports as much. MERGED shows the merge timestamp; OPEN shows the
// mergeability gate plus a pass/total check tally, with fail/pending counts
// appended only when nonzero.
func (s *PRStatus) Line(branch string) string {
	if s == nil {
		return fmt.Sprintf("no open PR for %s\n", branch)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s", s.Number, s.State)
	switch s.State {
	case "MERGED":
		if s.MergedAt != "" {
			fmt.Fprintf(&b, " merged=%s", s.MergedAt)
		}
	case "OPEN":
		if s.IsDraft {
			b.WriteString(" draft")
		}
		if s.Mergeable != "" {
			fmt.Fprintf(&b, " mergeable=%s", s.Mergeable)
		}
		if s.MergeStateStatus != "" {
			fmt.Fprintf(&b, " gate=%s", s.MergeStateStatus)
		}
		total := s.Pass + s.Fail + s.Pending
		fmt.Fprintf(&b, " checks=%d/%d", s.Pass, total)
		if s.Fail > 0 {
			fmt.Fprintf(&b, " fail=%d", s.Fail)
		}
		if s.Pending > 0 {
			fmt.Fprintf(&b, " pending=%d", s.Pending)
		}
	}
	b.WriteByte('\n')
	return b.String()
}
