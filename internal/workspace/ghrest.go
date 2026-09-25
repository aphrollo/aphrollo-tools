package workspace

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ghtransport"
)

// `gh pr view`/`create`/`merge` resolve rich fields (mergeable,
// mergeStateStatus, statusCheckRollup, --fill's derived title) through
// GitHub's GraphQL API, which some environments refuse outright — a Claude
// Code cloud container answers `gh pr view` with "HTTP 403: GitHub GraphQL
// is not available from Claude Code sessions; use the REST API" even though
// gh itself is installed and authenticated (#880). The REST equivalents
// below (`gh api repos/{owner}/{repo}/pulls…`) work everywhere GraphQL does
// AND everywhere it is blocked, so every gh-pr-view/create/merge call in
// this package goes through them unconditionally now — simpler than
// detecting the transport and carrying two code paths, and the issue itself
// allows it ("always if that's simpler and equivalent"). `gh api` still
// resolves `{owner}/{repo}` from the origin remote itself (a local git-config
// read, not a network call), so no separate repo lookup is needed.
//
// `gh pr checks`'s REST equivalent (commits/{sha}/check-runs and .../status)
// was already in place before this issue (ghChecksAt) — only view/create/merge
// needed converting.

// requireGH refuses up front, before a verb pushes or spends a mutation
// measurement, when gh cannot even answer a REST call — the "fail loud at
// the verb" half of #880. It does not require GraphQL: every verb-level gh
// call here goes through REST. A package var (bound to requireGHReal) so a
// test that does not care about gh's presence — the vast majority, which
// stub the individual gh seams (ghViewPR, ghCreatePR, …) directly — is not
// forced to also arrange a working `gh` on PATH.
var requireGH = requireGHReal

func requireGHReal() error {
	p := ghtransport.Run()
	if p.Ready() {
		return nil
	}
	return fmt.Errorf("gh is not ready: %s", p.FixLine())
}

// githubOwnerRepo splits origin's remote URL into owner/repo — read from git
// config, not gh, so it costs no network round trip and works even when gh
// itself cannot resolve anything yet. ok is false for a non-GitHub remote.
func githubOwnerRepo(wt string) (owner, repo string, ok bool) {
	web := branchURLBase(wt)
	if web == "" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(web, "https://github.com/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// branchURLBase is branchURL's repo-only half (no "/tree/<branch>" suffix),
// factored out so githubOwnerRepo shares the exact same remote resolution
// and normalization branchURL already uses.
func branchURLBase(wt string) string {
	// stderr-ok: a failed `remote get-url` just means "not a github remote"; the exit code alone is the whole signal
	out, err := exec.Command("git", "-C", wt, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return normalizeGitHubURL(strings.TrimSpace(string(out)))
}

// ghAPIPull is the REST "Get/List a pull request" shape — only the fields
// the workspace verbs read. REST's vocabulary differs from GraphQL's: State
// is "open"/"closed" (merged is its own bool, not a third state value),
// Mergeable is a tri-state bool (nil while GitHub is still computing it, the
// same meaning as GraphQL's "UNKNOWN"), and MergeableState uses GitHub's own
// lowercase words (clean/dirty/blocked/behind/unstable/draft/unknown/…)
// which the accessor methods below uppercase to match what this package's
// callers already expect from the GraphQL-era fields they replace.
type ghAPIPull struct {
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
}

// state renders the same three-way vocabulary GraphQL's `state` field used:
// OPEN | MERGED | CLOSED.
func (p *ghAPIPull) state() string {
	if p.Merged {
		return "MERGED"
	}
	return strings.ToUpper(p.State)
}

// mergeableWord renders GraphQL's MERGEABLE | CONFLICTING | UNKNOWN from
// REST's tri-state bool.
func (p *ghAPIPull) mergeableWord() string {
	if p.Mergeable == nil {
		return "UNKNOWN"
	}
	if *p.Mergeable {
		return "MERGEABLE"
	}
	return "CONFLICTING"
}

// mergeStateStatus renders GraphQL's MergeStateStatus vocabulary
// (CLEAN/DIRTY/BLOCKED/BEHIND/UNSTABLE/DRAFT/UNKNOWN/…) from REST's
// lowercase mergeable_state, which already uses the same words.
func (p *ghAPIPull) mergeStateStatus() string {
	if p.MergeableState == "" {
		return "UNKNOWN"
	}
	return strings.ToUpper(p.MergeableState)
}

func (p *ghAPIPull) info() *PRInfo {
	return &PRInfo{
		Number: p.Number, URL: p.HTMLURL, State: p.state(),
		IsDraft: p.Draft, Mergeable: p.mergeableWord(), MergeStateStatus: p.mergeStateStatus(),
	}
}

// ghAPIFindPR resolves the most recent PR for branch (any state) via REST's
// list-pulls endpoint, returning (0, false, nil) when none exists — the
// absence signal `gh pr view`'s GraphQL-era callers used to read off gh's
// own "no pull requests found" message text; REST just returns an empty
// array, so there is no message to sniff any more.
func ghAPIFindPR(wt, branch string) (int, bool, error) {
	owner, repo, ok := githubOwnerRepo(wt)
	if !ok {
		return 0, false, fmt.Errorf("origin is not a github remote in %s", wt)
	}
	out, err := ghCombinedOutput(wt, "api", "repos/"+owner+"/"+repo+"/pulls",
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

// ghAPIGetPR fetches the single pull's full REST detail — the fields the
// list endpoint above never carries (mergeable, mergeable_state, head.sha).
func ghAPIGetPR(wt string, number int) (*ghAPIPull, error) {
	owner, repo, ok := githubOwnerRepo(wt)
	if !ok {
		return nil, fmt.Errorf("origin is not a github remote in %s", wt)
	}
	out, err := ghCombinedOutput(wt, "api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number))
	if err != nil {
		return nil, fmt.Errorf("gh api pulls/%d: %v: %s", number, err, strings.TrimSpace(string(out)))
	}
	var p ghAPIPull
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("parsing gh api pulls/%d: %w", number, err)
	}
	return &p, nil
}

// ghAPIViewByBranch is the REST replacement for `gh pr view <branch>`:
// (nil, nil) means no PR exists for branch, matching every caller's existing
// absence handling.
func ghAPIViewByBranch(wt, branch string) (*ghAPIPull, error) {
	n, ok, err := ghAPIFindPR(wt, branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return ghAPIGetPR(wt, n)
}

// ghAPIViewByRef is the REST replacement for `gh pr view <ref>`, where ref is
// either a branch name or a PR number — `gh pr view` itself accepts both.
func ghAPIViewByRef(wt, ref string) (*ghAPIPull, error) {
	if n, err := strconv.Atoi(ref); err == nil {
		return ghAPIGetPR(wt, n)
	}
	return ghAPIViewByBranch(wt, ref)
}

// fillTitleBody derives a title/body the way `gh pr create --fill` would for
// the common case, without needing GraphQL: a branch with exactly one commit
// ahead of base uses that commit's subject/body; more than one lists each
// subject as a bullet under the branch name as title. This is an
// approximation of gh's own --fill (which also considers an issue/PR
// template), good enough for the default "no --title" path.
func fillTitleBody(wt, base, branch string) (title, body string) {
	// stderr-ok: a failed `git log` here just falls back to the branch name as title; the exit code alone is the whole signal
	out, err := exec.Command("git", "-C", wt, "log", "--reverse", "--format=%H", base+".."+branch).Output()
	if err != nil {
		return branch, ""
	}
	shas := strings.Fields(string(out))
	if len(shas) == 1 {
		// stderr-ok: a failed subject/body read here just falls back to "" — the exit code alone is the whole signal
		subj, _ := exec.Command("git", "-C", wt, "log", "-1", "--format=%s", shas[0]).Output()
		// stderr-ok: same as above — a failed body read falls back to ""
		bod, _ := exec.Command("git", "-C", wt, "log", "-1", "--format=%b", shas[0]).Output()
		return strings.TrimSpace(string(subj)), strings.TrimSpace(string(bod))
	}
	var b strings.Builder
	for _, sha := range shas {
		// stderr-ok: a failed subject read here just omits that bullet — the exit code alone is the whole signal
		subj, _ := exec.Command("git", "-C", wt, "log", "-1", "--format=%s", sha).Output()
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(string(subj)))
	}
	return branch, b.String()
}
