package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// `gh pr view`/`create`/`merge` resolve rich fields (mergeable,
// mergeStateStatus, statusCheckRollup, --fill's derived title) through
// GitHub's GraphQL API, which some sandboxed agent environments refuse
// outright — one answers `gh pr view` with "HTTP 403: GitHub GraphQL is not
// available from Claude Code sessions; use the REST API" even though gh
// itself is installed and authenticated (#880). The REST equivalents below
// (`gh api repos/{owner}/{repo}/pulls…`) work everywhere GraphQL does AND
// everywhere it is blocked, so every PR read, open and merge goes through
// them unconditionally. The one write with no portable REST route, marking a
// PR ready, tries gh's own `pr ready` first and falls back to the one
// sandboxed environment's REST route only when GraphQL is specifically blocked.

// apiPull is the REST "Get/List a pull request" shape: only the fields the
// verbs read. REST's vocabulary differs from GraphQL's: State is
// "open"/"closed" (merged is its own bool, not a third state value),
// Mergeable is a tri-state bool (nil while GitHub is still computing it, the
// same meaning as GraphQL's "UNKNOWN"), and MergeableState uses GitHub's own
// lowercase words which the accessors below uppercase to match the words the
// callers already expect.
type apiPull struct {
	Number         int    `json:"number"`
	HTMLURL        string `json:"html_url"`
	State          string `json:"state"` // open | closed
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergedAt       string `json:"merged_at"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

// state renders the three-way vocabulary GraphQL's `state` field used:
// OPEN | MERGED | CLOSED.
func (p *apiPull) state() string {
	if p.Merged {
		return "MERGED"
	}
	return strings.ToUpper(p.State)
}

// mergeableWord renders MERGEABLE | CONFLICTING | UNKNOWN from REST's
// tri-state bool.
func (p *apiPull) mergeableWord() string {
	if p.Mergeable == nil {
		return "UNKNOWN"
	}
	if *p.Mergeable {
		return "MERGEABLE"
	}
	return "CONFLICTING"
}

// mergeStateStatus renders the MergeStateStatus vocabulary from REST's
// lowercase mergeable_state, which already uses the same words.
func (p *apiPull) mergeStateStatus() string {
	if p.MergeableState == "" {
		return "UNKNOWN"
	}
	return strings.ToUpper(p.MergeableState)
}

func (p *apiPull) pr() *host.PR {
	return &host.PR{
		Number: p.Number, URL: p.HTMLURL, State: p.state(),
		IsDraft: p.Draft, Mergeable: p.mergeableWord(), MergeStateStatus: p.mergeStateStatus(),
		HeadSHA: p.Head.SHA, HeadRef: p.Head.Ref, BaseRef: p.Base.Ref, BaseRepo: p.Base.Repo.FullName,
		MergedAt: p.MergedAt,
	}
}

// FindPR resolves the newest PR for branch (any state) via REST's list-pulls
// endpoint, answering found=false when none exists: REST just returns an empty
// array, so there is no message text to sniff for the absence.
func (g *GitHub) FindPR(branch string) (int, bool, error) {
	owner, repo, err := g.ownerRepo()
	if err != nil {
		return 0, false, err
	}
	// --method GET: gh api sends -f fields as a POST body otherwise, and a
	// POST to the pulls list is a create call that fails with HTTP 422.
	out, err := g.gh("api", "--method", "GET", "repos/"+owner+"/"+repo+"/pulls",
		"-f", "head="+owner+":"+branch, "-f", "state=all", "-f", "sort=created", "-f", "direction=desc",
		"--jq", ".[0].number // empty")
	if err != nil {
		return 0, false, fmt.Errorf("gh api pulls (head=%s): %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, false, nil
	}
	n, convErr := strconv.Atoi(s)
	if convErr != nil {
		return 0, false, fmt.Errorf("gh api pulls: unexpected number %q", s)
	}
	return n, true, nil
}

// PR fetches the single pull's full REST detail: the fields the list endpoint
// never carries (mergeable, mergeable_state, head.sha).
func (g *GitHub) PR(number int) (*host.PR, error) {
	owner, repo, err := g.ownerRepo()
	if err != nil {
		return nil, err
	}
	out, err := g.gh("api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number))
	if err != nil {
		return nil, fmt.Errorf("gh api pulls/%d: %v: %s", number, err, strings.TrimSpace(string(out)))
	}
	var p apiPull
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("parsing gh api pulls/%d: %w", number, err)
	}
	return p.pr(), nil
}

// PRByBranch is the REST replacement for `gh pr view <branch>`: nil, nil means
// no PR exists for branch.
func (g *GitHub) PRByBranch(branch string) (*host.PR, error) {
	n, ok, err := g.FindPR(branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return g.PR(n)
}

// PRByRef is the REST replacement for `gh pr view <ref>`, where ref is either a
// branch name or a PR number, as gh accepts both.
func (g *GitHub) PRByRef(ref string) (*host.PR, error) {
	if n, err := strconv.Atoi(ref); err == nil {
		return g.PR(n)
	}
	return g.PRByBranch(ref)
}

// number is the PR for branch, or the refusal that there is none.
func (g *GitHub) number(branch string) (int, error) {
	n, found, err := g.FindPR(branch)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("gh api pulls: no PR found for %s", branch)
	}
	return n, nil
}

// PRText is the title and body the PR for branch holds now.
func (g *GitHub) PRText(branch string) (title, body string, err error) {
	out, err := g.gh("pr", "view", "--json", "title,body", "--", branch)
	if err != nil {
		return "", "", fmt.Errorf("gh pr view %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	var pr struct{ Title, Body string }
	if err := json.Unmarshal(out, &pr); err != nil {
		return "", "", fmt.Errorf("parsing gh pr view: %w", err)
	}
	return pr.Title, pr.Body, nil
}

// OpenPR opens the PR over REST.
func (g *GitHub) OpenPR(req host.OpenRequest) (*host.PR, error) {
	owner, repo, err := g.ownerRepo()
	if err != nil {
		return nil, err
	}
	args := []string{"api", "repos/" + owner + "/" + repo + "/pulls", "-X", "POST",
		"-f", "base=" + req.Base, "-f", "head=" + req.Branch,
		"-f", "title=" + req.Title, "-f", "body=" + req.Body}
	if req.Draft {
		args = append(args, "-F", "draft=true")
	}
	out, err := g.gh(args...)
	if err != nil {
		return nil, fmt.Errorf("gh api pulls (create): %v\n%s", err, strings.TrimSpace(string(out)))
	}
	var p apiPull
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("parsing gh api pulls (create): %w", err)
	}
	return p.pr(), nil
}

// EditBody sets a PR's body: a real, portable REST endpoint on ordinary
// GitHub (PATCH /repos/{owner}/{repo}/pulls/{number}), no fallback needed.
func (g *GitHub) EditBody(branch, body string) error {
	owner, repo, err := g.ownerRepo()
	if err != nil {
		return err
	}
	n, err := g.number(branch)
	if err != nil {
		return err
	}
	out, err := g.gh("api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, n), "-X", "PATCH", "-f", "body="+body)
	if err != nil {
		return fmt.Errorf("gh api pulls edit (body): %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// graphqlBlockedMarker is the text a sandboxed agent environment's proxy adds
// to gh's own error when a call it routed through GraphQL was refused
// outright (#880's "HTTP 403: GitHub GraphQL is not available from Claude
// Code sessions; use the REST API"). It is the ONE signal that justifies
// trying the sandbox-only REST fallback of MarkReady: any other gh failure
// (auth, network, no such PR) propagates as it is, never masked by a fallback.
const graphqlBlockedMarker = "graphql is not available"

// GraphQLBlocked reports whether gh's output says the environment refuses
// GraphQL.
func GraphQLBlocked(out string) bool {
	return strings.Contains(strings.ToLower(out), graphqlBlockedMarker)
}

// MarkReady takes the PR for branch out of draft. Marking a PR ready has no
// portable REST endpoint on ordinary GitHub: it is GraphQL-only, which is what
// gh's own `pr ready` drives, so it is tried FIRST and is the whole answer on a
// normal GitHub. Only when that fails with GraphQL specifically blocked does it
// fall back to `pulls/{n}/ccr/ready_for_review`, a REST route that exists only
// in that one sandboxed environment's proxy.
func (g *GitHub) MarkReady(branch string) error {
	out, err := g.gh("pr", "ready", "--", branch)
	if err == nil {
		return nil
	}
	if !GraphQLBlocked(string(out)) {
		return fmt.Errorf("gh pr ready: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return g.markReadySandbox(branch)
}

func (g *GitHub) markReadySandbox(branch string) error {
	owner, repo, err := g.ownerRepo()
	if err != nil {
		return err
	}
	n, err := g.number(branch)
	if err != nil {
		return err
	}
	out, err := g.gh("api", fmt.Sprintf("repos/%s/%s/pulls/%d/ccr/ready_for_review", owner, repo, n), "-X", "POST")
	if err != nil {
		return fmt.Errorf("gh api pulls ready_for_review (sandbox fallback): %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// summaryFields is the field list asked of `gh pr view --json`.
const summaryFields = "number,url,createdAt,mergedAt,headRefName"

// Summary is the PR as the retrospective reads it.
func (g *GitHub) Summary(number int) (*host.Summary, error) {
	out, err := g.gh("pr", "view", strconv.Itoa(number), "--json", summaryFields)
	if err != nil {
		return nil, fmt.Errorf("gh pr view: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var s host.Summary
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("gh pr view: %w", err)
	}
	return &s, nil
}
