package workspace

import (
	"encoding/json"
	"fmt"
	"os/exec"
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

// ghViewPRStatus is the seam over `gh pr view`, a package var so tests drive the
// rendering without gh or the network. It returns (nil, nil) when the branch has
// no PR — the "nothing to report" signal, not an error (mirrors ghViewPR).
var ghViewPRStatus = func(wt, branch string) (*PRStatus, error) {
	cmd := exec.Command("gh", "pr", "view",
		"--json", "number,state,isDraft,mergedAt,mergeable,mergeStateStatus,statusCheckRollup", "--", branch)
	cmd.Dir = wt
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // no PR for the branch
	}
	var raw struct {
		Number           int          `json:"number"`
		State            string       `json:"state"`
		IsDraft          bool         `json:"isDraft"`
		MergedAt         string       `json:"mergedAt"`
		Mergeable        string       `json:"mergeable"`
		MergeStateStatus string       `json:"mergeStateStatus"`
		Rollup           []checkEntry `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view: %w", err)
	}
	if raw.Number == 0 {
		return nil, nil
	}
	s := &PRStatus{
		Number:           raw.Number,
		State:            raw.State,
		IsDraft:          raw.IsDraft,
		MergedAt:         raw.MergedAt,
		Mergeable:        raw.Mergeable,
		MergeStateStatus: raw.MergeStateStatus,
	}
	for _, c := range raw.Rollup {
		switch classifyCheck(c) {
		case "pass":
			s.Pass++
		case "fail":
			s.Fail++
		default:
			s.Pending++
		}
	}
	return s, nil
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
