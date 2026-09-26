package workspace

import (
	"fmt"
	"io"
	"strings"
)

// graphqlBlockedMarker is the text a sandboxed agent environment's proxy adds
// to gh's own error when a call it routed through GraphQL was refused
// outright (#880's "HTTP 403: GitHub GraphQL is not available from Claude
// Code sessions; use the REST API"). It is the ONE signal that justifies
// trying this package's sandbox-only REST fallback below — any other gh
// failure (auth, network, no such PR) must propagate as-is, never masked by
// a fallback attempt.
const graphqlBlockedMarker = "graphql is not available"

func graphqlBlocked(out string) bool {
	return strings.Contains(strings.ToLower(out), graphqlBlockedMarker)
}

// ghReadyPR is the seam over marking a branch's draft PR ready for review, a
// package var so tests drive submit's flip logic without gh or the network.
//
// Marking a PR ready has no portable REST endpoint on ordinary GitHub — it
// is GraphQL-only (markPullRequestReadyForReview), which is exactly what
// gh's own `pr ready` already drives, so it is tried FIRST and is the whole
// answer on a normal GitHub. Only when that call fails with GraphQL
// specifically blocked (#880) does this fall back to
// `pulls/{n}/ccr/ready_for_review`, a REST route that exists ONLY in that one
// sandboxed environment's proxy — trying it first, or on any other GitHub,
// would just be a 404.
var ghReadyPR = func(wt, branch string) error {
	out, err := ghCombinedOutput(wt, "pr", "ready", "--", branch)
	if err == nil {
		return nil
	}
	if !graphqlBlocked(string(out)) {
		return fmt.Errorf("gh pr ready: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return ghReadyPRSandboxFallback(wt, branch)
}

// ghReadyPRSandboxFallback is the non-portable half of ghReadyPR — see its
// doc comment for why it is only ever tried second.
func ghReadyPRSandboxFallback(wt, branch string) error {
	owner, repo, ok := githubOwnerRepo(wt)
	if !ok {
		return fmt.Errorf("origin is not a github remote in %s", wt)
	}
	n, found, err := ghAPIFindPR(wt, branch)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("gh api pulls: no PR found for %s", branch)
	}
	out, err := ghCombinedOutput(wt, "api", fmt.Sprintf("repos/%s/%s/pulls/%d/ccr/ready_for_review", owner, repo, n), "-X", "POST")
	if err != nil {
		return fmt.Errorf("gh api pulls ready_for_review (sandbox fallback): %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ghEditPRBody is the seam over setting a PR's body, so the submit summary
// lands on the PR. A package var so tests observe the body without gh.
//
// Unlike ready-for-review, updating a PR's body IS a real, portable REST
// endpoint on ordinary GitHub (`PATCH /repos/{owner}/{repo}/pulls/{number}`
// with a body field) — no fallback needed, so this goes straight to REST.
var ghEditPRBody = func(wt, branch, body string) error {
	owner, repo, ok := githubOwnerRepo(wt)
	if !ok {
		return fmt.Errorf("origin is not a github remote in %s", wt)
	}
	n, found, err := ghAPIFindPR(wt, branch)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("gh api pulls: no PR found for %s", branch)
	}
	out, err := ghCombinedOutput(wt, "api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, n), "-X", "PATCH", "-f", "body="+body)
	if err != nil {
		return fmt.Errorf("gh api pulls edit (body): %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Submit is the coder's ONE-SHOT handoff that moves a card in_progress -> review.
// It pushes (idempotent) and reuses that push's own CI-state and mergeable
// reads (the ONE gh check-state read and the ONE mergeable poll per submit —
// see Push.Apply's prInfo/ci fields — so the caller needs no extra call and
// submit itself makes no second one), and is the SOLE opener of the PR: when
// none exists yet it OPENS ONE READY (not draft) so CI fires
// exactly once, at the handoff; when a draft already exists (a legacy or
// in-flight PR) it flips it ready; when one is already ready it is a no-op
// [skip]. The handoff happens unconditionally — on green, pending, red, or a
// merge conflict alike — and any remaining problem is repaired by a follow-up
// `push` to the same PR (which stays ready) — never a re-submit. The server-side
// AllOpenGreen gate (non-draft + ci_state=success) is what holds the reviewer
// until CI is actually green, so opening/flipping early never arms review
// prematurely; holding a draft back instead stranded tickets, since the coder's
// turn ends before it can re-run submit. Only a real failure (the gh open/flip
// call itself) is fatal. Re-callable, idempotent. It replaces the old `ready`
// verb (kept as a hidden alias).
type Submit struct {
	Target  *Target
	Summary string
	// Skip is the --skip-mutants override of the measurement the PR opens
	// behind (premutants.go).
	Skip SkipMutants
	push *Push
}

// SubmitPlan resolves the push stage and snapshots the request. It refuses a
// detached HEAD; the CI/PR state surfaces at Apply (it needs gh to know).
func SubmitPlan(t *Target, summary string) (*Submit, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before submitting", t.Worktree)
	}
	p, err := PushPlan(t, false)
	if err != nil {
		return nil, err
	}
	return &Submit{Target: t, Summary: summary, push: p}, nil
}

// Render previews the submit. The dry-run is explicit that the flip is gated on
// green CI and won't run here.
func (s *Submit) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace submit: %s -> review  (cwd %s)\n", s.Target.Branch, s.Target.Worktree)
	if !apply {
		fmt.Fprintf(&b, "  push (idempotent), then open a READY PR (none exists yet) or flip an existing\n")
		fmt.Fprintf(&b, "  draft to in-review — the one-shot handoff\n")
		fmt.Fprintf(&b, "  opens/flips on any CI state — review arms server-side once CI is green\n")
		fmt.Fprintf(&b, "  red CI / merge conflict: still opened/flipped, fix via a follow-up push (no re-submit)\n")
		fmt.Fprintf(&b, "\nrun again without --dry to submit.\n")
	}
	return b.String()
}

// Apply pushes, gates on CI, and on green flips the PR ready + sets its body.
// The receipt is stateful so no follow-up gh call is needed.
func (s *Submit) Apply(stdout, stderr io.Writer) error {
	if err := requireGH(); err != nil {
		return err
	}
	wt, branch := s.Target.Worktree, s.Target.Branch
	// How many commits this push delivers, captured BEFORE the push (after it the
	// remote ref equals HEAD). push.ahead is set when an upstream/remote branch
	// exists; for a brand-new branch it is "" — count against the default base.
	newCommits := s.push.ahead
	if newCommits == "" {
		newCommits = aheadCount(wt, "origin/"+resolveDefaultBranch(wt))
	}

	// Push is idempotent — a re-driven submit re-pushes a no-op and reuses the PR.
	// Run it QUIETLY (its own receipt is suppressed) so submit emits exactly one
	// consolidated receipt with no duplicate pr-url:/ci lines.
	if err := s.push.Apply(io.Discard, stderr); err != nil {
		return err
	}

	// Push.Apply above just performed this exact bounded mergeable poll (to
	// decide its own CONFLICT/unknown line, suppressed here via io.Discard) —
	// reuse its cached result rather than re-polling GitHub a second time.
	info, err := s.push.prInfo, s.push.prInfoErr
	if err != nil {
		return err
	}
	// Submit is the SOLE opener: when no PR exists yet, open one READY (not
	// draft) here — the handoff is the moment CI should start. The freshly
	// created info has no Mergeable/MergeStateStatus (gh never asked), which
	// mergeUnknown/isConflicting already treat as "not computed" rather than a
	// false conflict/unknown, so no extra re-read is needed.
	opened := false
	if info == nil {
		base := resolveDefaultBranch(wt)
		if err := mutantsBeforePR(wt, base, s.Skip, stdout, stderr); err != nil {
			return err
		}
		title, body, cerr := closureChecksBeforePR(wt, base, branch, "", "", stdout)
		if cerr != nil {
			return cerr
		}
		info, err = ghCreatePR(wt, PRCreate{Base: base, Branch: branch, Title: title, Body: body, Draft: false})
		if err != nil {
			return err
		}
		opened = true
	}

	// Likewise reuse Push's own CI read instead of a second `gh pr checks` call.
	ci, err := s.push.ci, s.push.ciErr
	if err != nil {
		return err
	}
	conflict := isConflicting(info)
	// Mergeability never resolved within the bound — say so rather than imply a
	// clean branch. Non-blocking; a follow-up push re-resolves it.
	if mergeUnknown(info) {
		fmt.Fprintf(stdout, "mergeable: unknown — push to recheck\n")
	}

	// The handoff is ONE-SHOT and unconditional: open/flip to ready now. The coder
	// signals "done" exactly once; ANY remaining problem — red CI, a merge conflict,
	// or CI still pending — is repaired by a follow-up `push` to the SAME PR, which
	// stays ready, with no re-submit. Holding a draft back instead stranded
	// tickets: the coder ends its turn before it can re-run submit. The server-side
	// AllOpenGreen gate (non-draft + ci_state success) holds the reviewer until CI is
	// actually green, and a conflicted branch can't green, so opening/flipping early
	// never arms review prematurely. Only a real failure (the gh open/flip call
	// itself) is fatal. A PR opened ready here (opened==true) needs no flip.
	flipped := false
	if !opened && info.IsDraft {
		if err := ghReadyPR(wt, branch); err != nil {
			return err
		}
		info.IsDraft = false
		flipped = true
	}

	// Body is best-effort cosmetic: a failure after a good flip must NOT fail the
	// submit — the PR is already in review. Warn and carry on.
	if strings.TrimSpace(s.Summary) != "" {
		if err := ghEditPRBody(wt, branch, s.Summary); err != nil {
			fmt.Fprintf(stdout, "warning: PR body not updated (%v) — handoff still succeeded\n", err)
		}
	}

	// Stateful receipt: never surfaces the literal ready_for_review. A re-run on an
	// already-ready PR is the idempotent [skip] path (no open, no re-flip).
	switch {
	case opened:
		fmt.Fprintf(stdout, "opened PR #%d ready for review: %s\n", info.Number, info.URL)
	case flipped:
		fmt.Fprintf(stdout, "submitted PR #%d  draft -> in review\n", info.Number)
	default:
		fmt.Fprintf(stdout, "already in review [skip]  PR #%d %s\n", info.Number, info.URL)
	}
	s.receiptTail(stdout, info, newCommits, ci, conflict)
	// Raise the first-class readiness signal at the api's internal submit endpoint
	// on the open and [skip] paths alike: neither ever fires a ready_for_review
	// webhook (a freshly opened PR is created ready outright; an already-ready PR
	// never re-drafts), so this endpoint call is the ONLY thing that arms review
	// there. On the flip path the webhook is redundant defense. Best-effort — a
	// failure warns but never fails the handoff.
	raiseSubmitSignal(stdout, !flipped)
	return nil
}

// receiptTail prints the shared post-state lines for both the flipped and the
// already-ready paths: sync state, the CI/conflict guidance, handoff, url, and
// the relay pr-* lines. The guidance is honest about what still blocks review:
// a conflict (CI can't run) says rebase+push; red says push a fix; pending says
// review arms when green. The server-side AllOpenGreen gate, not this verb, is
// what actually holds the reviewer.
func (s *Submit) receiptTail(stdout io.Writer, info *PRInfo, newCommits string, ci CIStatus, conflict bool) {
	if newCommits != "" && newCommits != "0" {
		fmt.Fprintf(stdout, "  pushed %s new commit(s)\n", newCommits)
	} else {
		fmt.Fprintf(stdout, "  already in sync\n")
	}
	switch {
	case conflict:
		fmt.Fprintf(stdout, "  merge conflict — rebase onto %s + push (review arms when green)\n", resolveDefaultBranch(s.Target.Worktree))
	case ci.State == "green":
		fmt.Fprintf(stdout, "  ci green\n")
	case ci.State == "red":
		fmt.Fprintf(stdout, "  ci red (%d failing) — push a fix (review arms when green)\n", ci.Failing)
	default: // pending | none
		fmt.Fprintf(stdout, "  ci %s — review arms when green\n", ci.State)
	}
	fmt.Fprintf(stdout, "  handoff in_progress -> review\n")
	fmt.Fprintf(stdout, "  %s\n", info.URL)
	reportPRState(stdout, info)
}
