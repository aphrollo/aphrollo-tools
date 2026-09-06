package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
// gh exits non-zero both for "no PR exists for this branch" (absence) and for
// a genuine failure — a network timeout (see #290's networkTimeoutErr), a
// missing gh, no auth. Only the FIRST is absence: isNoPRError distinguishes
// on gh's own message text (the same check ghPRState in prune.go already
// makes for the identical reason), and everything else propagates as an
// error. A stalled gh pr view used to hang forever and never reach this
// function at all; now that it returns promptly, silently reading ITS error
// as "no PR" would open a duplicate PR on a branch that already has one.
func ghViewPRReal(wt, branch string) (*PRInfo, error) {
	out, err := ghCombinedOutput(wt, "pr", "view", "--json", "number,url,state,isDraft,mergeable,mergeStateStatus", "--", branch)
	if err != nil {
		if isNoPRError(string(out)) {
			return nil, nil // absence-ok: gh's own no-PR message, checked above, not a blind swallow
		}
		return nil, fmt.Errorf("gh pr view %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	var info PRInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parsing gh pr view: %w", err)
	}
	if info.Number == 0 {
		return nil, nil
	}
	return &info, nil
}

var (
	ghCreatePR = func(wt string, req PRCreate) (*PRInfo, error) {
		// "--base=" / "--head=" attach the value to the flag so a branch name
		// can't be misparsed as a separate option (defense in depth behind
		// Slugify). --title/--body stay separate: their values are free text, not
		// branch names, and gh accepts a leading-dash value after a space.
		args := []string{"pr", "create", "--base=" + req.Base, "--head=" + req.Branch}
		switch {
		case req.Title != "":
			args = append(args, "--title", req.Title, "--body", req.Body)
		default:
			args = append(args, "--fill")
		}
		if req.Draft {
			args = append(args, "--draft")
		}
		out, err := ghCombinedOutput(wt, args...)
		if err != nil {
			return nil, fmt.Errorf("gh pr create: %v\n%s", err, strings.TrimSpace(string(out)))
		}
		url := firstURL(string(out))
		return &PRInfo{URL: url, Number: prNumberFromURL(url), State: "OPEN", IsDraft: req.Draft}, nil
	}
)

// CIStatus is the resolved CI state for a branch's PR: State is one of "green"
// (all checks passed), "red" (at least one failed), "pending" (still running or
// none reported yet), or "none" (no PR / no checks). Failing is the count of
// failed checks, surfaced in the blocked receipt.
type CIStatus struct {
	State   string // green | red | pending | none
	Failing int
}

// ghCIStatus is the seam over `gh pr checks` — a package var so submit/push tests
// drive the CI gate without gh or the network. The real implementation reads the
// branch PR's combined check state in the worktree's repo. Push.Apply is the
// ONLY caller that issues it during a submit; Submit reuses Push's cached
// result (p.ci/p.ciErr) rather than calling it again.
//
// `gh pr checks` exits 0 when all checks pass, 8 when checks are pending, and
// non-zero otherwise (failures). We parse its TSV rows for the per-check verdict
// to distinguish red from pending and to count failures, falling back to the
// exit code when the output is empty.
//
// ghCIStatusArgs builds the `gh pr checks --json state` argv. branch is
// guarded behind "--", matching every sibling gh call site in this package
// (ghEditPRBodyArgs, ghReadyPR in submit.go; ghViewPR and ghCreatePR's
// --head= here): gh's flag parser treats a branch starting with "-" as a
// flag reference regardless of position, and git ref names ARE allowed to
// start with "-" (see #160). "--json state" must come BEFORE "--": pflag
// stops recognizing flags the instant it sees "--", so "--" sits
// immediately before the trailing branch positional, never earlier.
func ghCIStatusArgs(branch string) []string {
	return []string{"pr", "checks", "--json", "state", "--", branch}
}

var ghCIStatus = func(wt, branch string) (CIStatus, error) {
	out, err := ghOutput(wt, ghCIStatusArgs(branch)...)
	if err == nil && len(strings.TrimSpace(string(out))) == 0 {
		return CIStatus{State: "none"}, nil
	}
	// --json emits an array of {state}; classify it. A non-zero exit with no
	// parseable JSON (e.g. no PR yet) is treated as "none", not an error, so the
	// caller stays re-callable.
	var rows []struct {
		State string `json:"state"`
	}
	if jerr := json.Unmarshal(out, &rows); jerr != nil || len(rows) == 0 {
		return CIStatus{State: "none"}, nil
	}
	failing, pending := 0, 0
	for _, r := range rows {
		switch strings.ToUpper(r.State) {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
			failing++
		default: // PENDING, QUEUED, IN_PROGRESS, EXPECTED, ""
			pending++
		}
	}
	switch {
	case failing > 0:
		return CIStatus{State: "red", Failing: failing}, nil
	case pending > 0:
		return CIStatus{State: "pending"}, nil
	default:
		return CIStatus{State: "green"}, nil
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
}

// PRPlan resolves the PR open without touching gh. An empty base resolves the
// repo's actual default branch (never assumed to be literally "main"); an
// empty title means "let gh fill it from the commits". It refuses a detached
// HEAD and requires the branch to be on origin (gh pr create needs it there).
func PRPlan(t *Target, base, title, body string, draft bool) (*PR, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before opening a PR", t.Worktree)
	}
	if base == "" {
		base = resolveDefaultBranch(t.Worktree)
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
	if existing, err := reuseOpenPR(p.Target.Worktree, p.Create.Branch); err != nil {
		return err
	} else if existing != nil {
		fmt.Fprintf(stdout, "PR #%d already open: %s\n", existing.Number, existing.URL)
		reportPRState(stdout, existing)
		return nil
	}
	info, err := ghCreatePR(p.Target.Worktree, p.Create)
	if err != nil {
		return err
	}
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

// firstURL returns the first whitespace-delimited token that looks like a URL —
// gh pr create prints the PR URL on its own line.
func firstURL(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "https://") || strings.HasPrefix(f, "http://") {
			return f
		}
	}
	return strings.TrimSpace(s)
}

// prNumberFromURL extracts the trailing number from a .../pull/<n> URL.
func prNumberFromURL(url string) int {
	i := strings.LastIndex(url, "/")
	if i < 0 || i+1 >= len(url) {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(url[i+1:], "%d", &n); err != nil {
		return 0
	}
	return n
}
