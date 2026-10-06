package github

import (
	"fmt"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github/ghtransport"
)

// OpenIssue opens one issue and answers its URL. gh prints the URL as its last
// line; an answer with none is refused, never taken for success.
func (g *GitHub) OpenIssue(r host.IssueRequest) (string, error) {
	out, err := g.gh(IssueArgs(r)...)
	if err != nil {
		return "", fmt.Errorf("gh issue: %w: %s", err, fit(strings.TrimSpace(string(out)), 400))
	}
	url := lastNonEmptyLine(string(out))
	if !strings.Contains(url, "/issues/") {
		return "", fmt.Errorf("gh issue create printed no issue URL: %q", fit(strings.TrimSpace(string(out)), 200))
	}
	return url, nil
}

// IssueArgs is the gh command line one issue is created with: whose tracker it
// lands in is decided here, which makes the routing testable without a network.
func IssueArgs(r host.IssueRequest) []string {
	args := []string{"issue", "create", "--title", r.Title, "--body", r.Body}
	if r.Repo != "" {
		args = append(args, "--repo", r.Repo)
	}
	for _, l := range r.Labels {
		args = append(args, "--label", l)
	}
	return args
}

// EnsureLabel creates a label that may not exist yet. `--force` makes it
// idempotent: it updates an existing label instead of failing.
func (g *GitHub) EnsureLabel(name, colour, description string) error {
	args := []string{"label", "create", name, "--force", "--color", colour}
	if description != "" {
		args = append(args, "--description", description)
	}
	out, err := g.gh(args...)
	if err != nil {
		return fmt.Errorf("gh label: %w: %s", err, fit(strings.TrimSpace(string(out)), 400))
	}
	return nil
}

// Probe says what this box can reach of GitHub through gh.
func (g *GitHub) Probe(withGraphQL bool) host.Probe {
	if withGraphQL {
		return ghtransport.Run()
	}
	return ghtransport.RunRESTOnly()
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// fit cuts s to at most n runes.
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var _ host.IssueCloser = (*GitHub)(nil)

// CloseIssue closes the issue, with the comment when there is one.
func (g *GitHub) CloseIssue(number int, comment string) error {
	args := []string{"issue", "close", fmt.Sprint(number)}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	if _, err := g.read(args...); err != nil {
		return err
	}
	return nil
}

var _ host.Identity = (*GitHub)(nil)

// Whoami is the login gh is authenticated as.
func (g *GitHub) Whoami() (string, error) {
	out, err := g.read("api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
