package workspace

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// PRInfo is the subset of a GitHub PR the verbs care about.
type PRInfo struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	State   string `json:"state"` // OPEN | MERGED | CLOSED
	IsDraft bool   `json:"isDraft"`
	// Mergeable is GitHub's async-computed merge verdict: MERGEABLE | CONFLICTING
	// | UNKNOWN. It is UNKNOWN for a short window right after a push until GitHub
	// finishes computing the merge ref — viewPRMergeable's bounded re-poll waits
	// it out so a conflicted branch is caught while the coder is still live.
	Mergeable string `json:"mergeable"`
	// MergeStateStatus is the finer-grained merge state: DIRTY (conflicts) |
	// BEHIND | BLOCKED | CLEAN | UNSTABLE | HAS_HOOKS | DRAFT | UNKNOWN. DIRTY
	// corroborates CONFLICTING.
	MergeStateStatus string `json:"mergeStateStatus"`
	// HeadSHA is the commit GitHub currently holds as the PR's head.
	HeadSHA string `json:"-"`
	// BaseRef is the branch the PR merges into; empty when GitHub did not say.
	BaseRef string `json:"-"`
	// BaseRepo is the repository the PR merges into, "owner/name"; empty when GitHub did not say.
	BaseRepo string `json:"-"`
}

// prStateWord maps a gh PR to the canonical lifecycle word the rlndx kanban git
// indicator renders: draft | open | merged | closed. A draft is OPEN on
// GitHub's side but a distinct card state, so it is split out here.
func prStateWord(info *PRInfo) string {
	switch strings.ToUpper(info.State) {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	default: // OPEN
		if info.IsDraft {
			return "draft"
		}
		return "open"
	}
}

// ghViewPR and ghCreatePR are the seam over the `gh` CLI. They are package vars
// so tests can drive pr's reuse-vs-create logic without gh or the network. The
// real implementations shell `gh` in the worktree.
//
// ghViewPR returns (nil, nil) when no PR exists for the branch — that is the
// signal to create one, not an error. It is a var (bound to ghViewPRReal) so
// tests can drive pr's reuse-vs-create logic without gh or the network.
var ghViewPR = ghViewPRReal

// ghViewPRReal is ghViewPR's real implementation, named so a test can call it
// directly regardless of what another test's stubGH last pointed the ghViewPR
// var at.
//
// Routed over REST (the host port's PRByBranch, #880) rather than `gh pr view`, which
// goes through GraphQL: a genuine failure (a network timeout, see #290's
// networkTimeoutErr; a missing gh; no auth) still propagates as an error, but
// absence is no longer read off gh's own message text — REST's list-pulls
// endpoint just answers an empty array, so there is no ambiguous non-zero
// exit to disambiguate at all. A stalled gh call used to hang forever and
// never reach this branch; now that it returns promptly, silently reading
// ITS error as "no PR" would open a duplicate PR on a branch that already has
// one.
func ghViewPRReal(wt, branch string) (*PRInfo, error) {
	p, err := hostFor(wt).PRByBranch(branch)
	if err != nil {
		return nil, err
	}
	return prInfoOf(p), nil // nil when the host has no PR for the branch
}

var (
	ghCreatePR = func(wt string, req PRCreate) (*PRInfo, error) {
		title, body := req.Title, req.Body
		if title == "" {
			// REST has no --fill; approximate gh's own derivation from the
			// branch's commits (fillTitleBody).
			title, body = fillTitleBody(wt, req.Base, req.Branch)
		}
		p, err := hostFor(wt).OpenPR(host.OpenRequest{Base: req.Base, Branch: req.Branch, Title: title, Body: body, Draft: req.Draft})
		if err != nil {
			return nil, err
		}
		return prInfoOf(p), nil
	}
)

// CIStatus is the resolved CI state for one commit: State is one of "green"
// (all checks passed), "red" (at least one failed) or "pending" (still running,
// or no check has started on the commit yet — NoRun) or "unavailable" (no
// check failed, but hosted CI never started NotStarted of the jobs, #1064).
// Failing is the count of failed checks, surfaced in the blocked receipt. SHA
// is the commit judged.
type CIStatus struct {
	State      string // green | red | pending | unavailable
	Failing    int
	NotStarted int
	SHA        string
	NoRun      bool // no check exists for SHA yet
	// Checks counts the check runs on SHA, Skipped those that concluded skipped.
	Checks, Skipped int
	// Cause says why a red was red: test, mutation or other (ciCause). "" unless red.
	Cause string
}

// Word renders the state for a receipt line: a commit CI has not reached reads
// "pending (no run yet for <sha>)", never the state of an earlier commit.
func (c CIStatus) Word() string {
	if c.State == "unavailable" {
		return fmt.Sprintf("unavailable: %d job(s) not started on %s", c.NotStarted, short(c.SHA))
	}
	if c.NoRun {
		return fmt.Sprintf("%s (no run yet for %s)", c.State, short(c.SHA))
	}
	return c.State
}

// ghCIStatus is the seam over the checks of ONE commit — a package var so
// submit/push/merge tests drive the CI gate without gh or the network. It
// reads commits/{sha}/check-runs and .../status (ghChecksAt), never the PR's
// head as GitHub currently holds it: right after a push that is still the
// previous commit, whose green would be reported for the new one. A commit no
// check has started on is pending with NoRun. Push.Apply is the ONLY caller
// that issues it during a submit; Submit reuses Push's cached result
// (p.ci/p.ciErr) rather than calling it again.
var ghCIStatus = func(wt, sha string) (CIStatus, error) {
	if sha == "" {
		return CIStatus{}, fmt.Errorf("no commit to read CI for")
	}
	runs, err := ghChecksAt(wt, sha)
	if err != nil {
		return CIStatus{}, err
	}
	failing, pending, notStarted, checks, skipped, reached := 0, 0, 0, 0, 0, false
	var failed []string
	for _, r := range runs {
		if r.SHA != sha {
			continue
		}
		reached = true
		checks++
		if strings.EqualFold(r.Conclusion, "skipped") {
			skipped++
		}
		switch classifyCheckRun(r) {
		case "fail":
			if r.NotStarted {
				notStarted++
				continue
			}
			failing++
			failed = append(failed, r.Name)
		case "pending":
			pending++
		}
	}
	switch {
	case !reached:
		return CIStatus{State: "pending", SHA: sha, NoRun: true}, nil
	case failing > 0:
		return CIStatus{State: "red", Failing: failing, SHA: sha, Cause: ciCause(failed)}, nil
	case pending > 0:
		return CIStatus{State: "pending", SHA: sha}, nil
	case notStarted > 0:
		return CIStatus{State: "unavailable", NotStarted: notStarted, SHA: sha}, nil
	default:
		return CIStatus{State: "green", SHA: sha, Checks: checks, Skipped: skipped}, nil
	}
}

// PRCreate is the resolved create request handed to the gh seam.
type PRCreate struct {
	Base   string
	Branch string
	Title  string
	Body   string
	Draft  bool
}

// PR is a resolved pull-request open for a worktree's branch. Apply is
// idempotent: an existing open PR for the branch is reported, never duplicated.
type PR struct {
	Target *Target
	Create PRCreate
	// Skip is the --skip-mutants override of the measurement a PR opens
	// behind (premutants.go).
	Skip SkipMutants
}

// PRPlan resolves the PR open without touching gh. An empty base resolves the
// repo's actual default branch (never assumed to be literally "main"); an
// empty title means "let gh fill it from the commits". It refuses a detached
// HEAD and requires the branch to be on origin (gh pr create needs it there).
func PRPlan(t *Target, base, title, body string, draft bool) (*PR, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before opening a PR", t.Worktree)
	}
	if err := undercoverCheck(t.Worktree, t.Branch, undercoverText{"PR title", title}, undercoverText{"PR body", body}); err != nil {
		return nil, err
	}
	if base == "" {
		def, err := needDefaultBranch(t.Worktree)
		if err != nil {
			return nil, err
		}
		base = def
	}
	return &PR{
		Target: t,
		Create: PRCreate{Base: base, Branch: t.Branch, Title: title, Body: body, Draft: draft},
	}, nil
}

// Render previews the PR open.
func (p *PR) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace pr: %s <- %s  (cwd %s)\n", p.Create.Base, p.Create.Branch, p.Target.Worktree)
	if apply {
		return b.String()
	}
	title := p.Create.Title
	if title == "" {
		title = "(filled from commits)"
	}
	fmt.Fprintf(&b, "  title: %s\n", title)
	if p.Create.Draft {
		fmt.Fprintf(&b, "  draft\n")
	}
	fmt.Fprintf(&b, "  an existing open PR for %s is reused, not duplicated\n", p.Create.Branch)
	fmt.Fprintf(&b, "\nrun again without --dry to open the PR (needs the branch pushed to origin).\n")
	return b.String()
}

// Apply reuses an existing OPEN PR or creates one, printing the URL either
// way. It goes through reuseOpenPR rather than ghViewPR directly: `gh pr
// view` returns the most recent PR for the branch regardless of state, so a
// MERGED or CLOSED PR is dead and must be treated as none — reused only when
// still OPEN.
func (p *PR) Apply(stdout, stderr io.Writer) error {
	if err := requireGH(); err != nil {
		return err
	}
	if existing, err := reuseOpenPR(p.Target.Worktree, p.Create.Branch); err != nil {
		return err
	} else if existing != nil {
		fmt.Fprintf(stdout, "PR #%d already open: %s\n", existing.Number, existing.URL)
		reportPRState(stdout, existing)
		return nil
	}
	if err := mutantsBeforePR(p.Target.Worktree, p.Create.Base, p.Skip, stdout, stderr); err != nil {
		return err
	}
	title, body, err := closureChecksBeforePR(p.Target.Worktree, p.Create.Base, p.Create.Branch, p.Create.Title, p.Create.Body, stdout)
	if err != nil {
		return err
	}
	p.Create.Title, p.Create.Body = title, body
	info, err := ghCreatePR(p.Target.Worktree, p.Create)
	if err != nil {
		return err
	}
	tdd.AppendEvent(tdd.Event{Kind: "pr_opened", Root: p.Target.Worktree, Verdict: "ok",
		Detail: map[string]string{"pr": strconv.Itoa(info.Number), "draft": strconv.FormatBool(info.IsDraft)}})
	draftWord := ""
	if info.IsDraft {
		draftWord = "draft "
	}
	if info.Number > 0 {
		fmt.Fprintf(stdout, "opened %sPR #%d: %s  (%s <- %s)\n", draftWord, info.Number, info.URL, p.Create.Base, p.Create.Branch)
	} else {
		fmt.Fprintf(stdout, "opened %sPR: %s  (%s <- %s)\n", draftWord, info.URL, p.Create.Base, p.Create.Branch)
	}
	reportPRState(stdout, info)
	return nil
}

// reuseOpenPR looks up branch's PR and returns it only when OPEN (draft or
// ready). Push never OPENS a PR — submit is the sole opener, so CI fires
// exactly once at handoff — so a dead (merged/closed) PR is treated the same as
// none: nothing for push to reuse. The branch must already be on origin (the
// caller pushes first).
func reuseOpenPR(wt, branch string) (*PRInfo, error) {
	if !remoteBranchExists(wt, branch) {
		return nil, fmt.Errorf("branch %s is not on origin — run: aphrollo workspace push", branch)
	}
	existing, err := ghViewPR(wt, branch)
	if err != nil {
		return nil, err
	}
	if existing != nil && strings.ToUpper(existing.State) == "OPEN" {
		return existing, nil
	}
	return nil, nil
}

// reportPRState prints the two machine-readable lines a coder relays into
// tickets_update(pr_url, pr_state) so the rlndx card's git indicator tracks the
// PR through draft → open → merged without a webhook.
func reportPRState(stdout io.Writer, info *PRInfo) {
	fmt.Fprintf(stdout, "pr-url: %s\npr-state: %s\n", info.URL, prStateWord(info))
}

// AllSkipped reports a head whose every check concluded skipped: no check ran.
func (c CIStatus) AllSkipped() bool { return c.Checks > 0 && c.Skipped == c.Checks }
