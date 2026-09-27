package escape

import (
	"net/url"
	"strings"
)

// The escape-closure judgment reads issue numbers out of free text (a PR
// body, a commit message). A number qualified with "owner/name#" names a
// SPECIFIC repo's tracker — GitHub's own syntax for a cross-repo reference —
// and only counts as one of THIS repo's own issues when that owner/name is
// repo's own remote. Telling the two apart, and telling gh's "no such local
// issue" refusal from a genuine failure to reach GitHub at all, are both here
// (issue #931).

// localOwnerRepo reads repo's own "owner/name" off its origin remote — what a
// qualified owner/name#N reference is compared against to tell a LOCAL
// closing reference from one naming another repo's tracker. ok is false for a
// non-GitHub remote, or a read that fails (no remote at all).
func localOwnerRepo(repo string) (ownerName string, ok bool) {
	out, err := gitRead(repo, "remote", "get-url", "origin")
	if err != nil {
		return "", false // absence-ok: no origin remote at all reads as "local repo unknown", the same conservative case as a non-GitHub one
	}
	return githubPathFromRemote(strings.TrimSpace(out))
}

// githubPathFromRemote extracts "owner/name" from a GitHub remote URL, either
// form (`https://github.com/owner/name(.git)` or
// `git@github.com:owner/name(.git)`).
func githubPathFromRemote(remote string) (string, bool) {
	remote = strings.TrimSuffix(remote, ".git")
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	u, err := url.Parse(remote)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", false
	}
	return path, true
}

// unresolvedIssueErrText is the wording gh's GraphQL layer prints when an
// issue number resolves against no issue in the repo it asked about — the
// one gh failure that means "not a local issue" rather than a genuine
// failure to reach GitHub at all (auth, network, rate limit).
const unresolvedIssueErrText = "Could not resolve to an issue or pull request"

// unresolvedIssueErr reports whether err is gh's specific "no such issue"
// refusal. A number that names no LOCAL issue cannot be an unclosed local
// escape, so this is a warning rather than a hard failure; every other gh
// error (auth, network, rate limit) is a real failure and still refuses.
func unresolvedIssueErr(err error) bool {
	// error-text-ok: gh's only signal that an issue number resolves to nothing in the repo it asked about is this GraphQL refusal's own wording — no sentinel or typed error gh hands back instead.
	return strings.Contains(err.Error(), unresolvedIssueErrText)
}
