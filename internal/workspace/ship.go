package workspace

import (
	"fmt"
	"io"
	"strings"
)

// Ship chains the three outward-bound atoms — commit → push → pr — behind one
// command so an agent that finished a change spends one tool call landing it
// instead of three. Each stage is the same resolved verb the discrete commands
// use, so behavior (the pre-commit gate, PR reuse, ahead-counts) is identical;
// ship only sequences them and stops at the first failure.
type Ship struct {
	commit *Commit
	push   *Push
	pr     *PR
}

// ShipRequest is the parsed input to ShipPlan.
type ShipRequest struct {
	Message  string // commit message (required)
	StageAll bool   // git add -A before committing
	NoVerify bool   // skip the pre-commit gate
	Reason   string // required alongside NoVerify — why the gate is being skipped
	Base     string // PR base ("" resolves the repo's default branch)
	Title    string // PR title ("" => filled from commits)
	Body     string // PR body
	Draft    bool   // open the PR as a draft
	// SkipMutants opens the PR without the measurement before it; it needs
	// a reason.
	SkipMutants SkipMutants
}

// ShipPlan resolves all three stages up front so the dry-run can show the whole
// sequence and an addressing/usage error surfaces before anything runs.
func ShipPlan(t *Target, req ShipRequest) (*Ship, error) {
	if err := req.SkipMutants.validate(); err != nil {
		return nil, err
	}
	c, err := CommitPlan(t, req.Message, req.StageAll, req.NoVerify, req.Reason)
	if err != nil {
		return nil, err
	}
	p, err := PushPlan(t, false)
	if err != nil {
		return nil, err
	}
	pr, err := PRPlan(t, req.Base, req.Title, req.Body, req.Draft)
	if err != nil {
		return nil, err
	}
	pr.Skip = req.SkipMutants
	return &Ship{commit: c, push: p, pr: pr}, nil
}

// Render previews all three stages.
func (s *Ship) Render(apply bool) string {
	if apply {
		return "workspace ship: commit -> push -> pr\n"
	}
	var b strings.Builder
	b.WriteString("workspace ship: commit -> push -> pr\n\n")
	b.WriteString(indent(s.commit.Render(false)))
	b.WriteString(indent(s.push.Render(false)))
	b.WriteString(indent(s.pr.Render(false)))
	b.WriteString("\nrun again without --dry to commit, push, and open the PR.\n")
	return b.String()
}

// Apply runs the three stages in order, stopping at the first failure. A failed
// stage leaves the worktree in a known state (e.g. committed but not pushed),
// which the discrete verbs can then resume.
func (s *Ship) Apply(stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "── commit ──\n")
	if err := s.commit.Apply(stdout, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "── push ──\n")
	if err := s.push.Apply(stdout, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "── pr ──\n")
	if err := s.pr.Apply(stdout, stderr); err != nil {
		return err
	}
	return nil
}

// indent prefixes each non-empty line with two spaces so the nested stage plans
// read as sub-blocks under the ship header.
func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
