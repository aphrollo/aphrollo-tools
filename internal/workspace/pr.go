package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// PRInfo is the subset of a GitHub PR the verbs care about.
type PRInfo struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	State   string `json:"state"` // OPEN | MERGED | CLOSED
	IsDraft bool   `json:"isDraft"`
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
// signal to create one, not an error.
var (
	ghViewPR = func(wt, branch string) (*PRInfo, error) {
		// gh resolves the repo from the worktree's origin. It exits non-zero when
		// no PR exists for the branch — absence, not a failure: return (nil, nil)
		// so Apply creates one. A real PR with malformed JSON is the only error.
		cmd := exec.Command("gh", "pr", "view", "--json", "number,url,state,isDraft", "--", branch)
		cmd.Dir = wt
		out, err := cmd.Output()
		if err != nil {
			return nil, nil
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
		cmd := exec.Command("gh", args...)
		cmd.Dir = wt
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("gh pr create: %v\n%s", err, strings.TrimSpace(string(out)))
		}
		url := firstURL(string(out))
		return &PRInfo{URL: url, Number: prNumberFromURL(url), State: "OPEN", IsDraft: req.Draft}, nil
	}
)

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

// PRPlan resolves the PR open without touching gh. base defaults to main; an
// empty title means "let gh fill it from the commits". It refuses a detached
// HEAD and requires the branch to be on origin (gh pr create needs it there).
func PRPlan(t *Target, base, title, body string, draft bool) (*PR, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before opening a PR", t.Worktree)
	}
	if base == "" {
		base = "main"
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
	fmt.Fprintf(&b, "\nrun again with --apply to open the PR (needs the branch pushed to origin).\n")
	return b.String()
}

// Apply reuses an existing open PR or creates one, printing the URL either way.
func (p *PR) Apply(stdout, stderr io.Writer) error {
	if !remoteBranchExists(p.Target.Worktree, p.Create.Branch) {
		return fmt.Errorf("branch %s is not on origin — run: aphrollo workspace push", p.Create.Branch)
	}
	if existing, err := ghViewPR(p.Target.Worktree, p.Create.Branch); err != nil {
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
