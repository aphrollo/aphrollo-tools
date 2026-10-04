package github

import (
	"net/url"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// NormalizeURL reduces a git remote URL to its https web base
// (https://github.com/owner/repo), stripping the .git suffix and any userinfo.
// "" for a remote that is not on github.com.
func NormalizeURL(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	// scp-like scheme (git@github.com:owner/repo) has no "://" and carries
	// no userinfo beyond the fixed "git@" — net/url does not parse this
	// form as a URL at all, so it keeps its own prefix check.
	if strings.HasPrefix(remote, "git@github.com:") {
		return "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	u, err := url.Parse(remote)
	if err != nil || u.Hostname() != "github.com" {
		return ""
	}
	switch u.Scheme {
	case "http", "https", "ssh":
	default:
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "https://github.com"
	}
	return "https://github.com/" + path
}

// OwnerRepo splits origin's remote URL into owner and repo, read from the
// remote's URL and not from gh, so it costs no round trip and works even when
// gh cannot resolve anything yet. ok is false for a non-GitHub remote.
func OwnerRepo(remote string) (owner, repo string, ok bool) {
	web := NormalizeURL(remote)
	if web == "" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(web, "https://github.com/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ownerRepo is origin's owner and repo, or the refusal naming the directory.
func (g *GitHub) ownerRepo() (owner, repo string, err error) {
	owner, repo, ok := OwnerRepo(g.origin())
	if !ok {
		return "", "", host.NotAHostRemote(g.dir)
	}
	return owner, repo, nil
}

// splitRepo reads "owner/name", or origin's owner and name when slug is empty.
func (g *GitHub) splitRepo(slug string) (owner, name string, err error) {
	if o, n, found := strings.Cut(slug, "/"); found && o != "" && n != "" {
		return o, n, nil
	}
	return g.ownerRepo()
}
