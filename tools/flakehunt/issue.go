package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/suite"
)

// defaultReproCount is the `-count` the reproduce command carries: high
// enough that a flake with any real reproduction rate shows up in one run,
// cheap enough to type into a terminal by hand.
const defaultReproCount = 50

// Filer opens or updates one GitHub issue per flaky test found in a run.
type Filer struct {
	// Repo is the checkout whose GitHub remote issues are opened against.
	Repo string
	// RunURL is this workflow run's own URL, quoted in every issue and
	// update so a reader can go straight to the log that found it.
	RunURL string
	// Label is the declared theme this repo's issue-labels list carries that
	// sits closest to flakiness — passed straight to `aphrollo issue`'s own
	// declared-label check.
	Label    string
	NewLabel bool
}

// TitleFor is the issue title a failing test files under. Fixed and
// reproducible from the test alone, so the SAME failing test always searches
// and matches the SAME open issue, run after run.
func TitleFor(f Failure) string {
	return fmt.Sprintf("flaky: %s (%s)", f.Test, f.Package)
}

// ReproCmd is the command a reader runs to reproduce f on their own box: the
// same package, race detector and shuffle seed the nightly run itself used.
func ReproCmd(f Failure) string {
	return fmt.Sprintf("go test ./%s -race -run '^%s$' -count=%d -shuffle=%s",
		f.Package, f.Test, defaultReproCount, f.Seed)
}

// BodyFor is the issue body (or update comment) for one failure: the seed
// that produced it, the command that reproduces it, the failure excerpt
// itself, and the run it was found in.
func BodyFor(f Failure, runURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Seed: `%s`\n\n", f.Seed)
	fmt.Fprintf(&b, "Reproduce: `%s`\n\n", ReproCmd(f))
	fmt.Fprintf(&b, "Failure excerpt:\n```\n%s```\n\n", f.Excerpt)
	fmt.Fprintf(&b, "Run: %s\n", runURL)
	return b.String()
}

// searchArgv is the `gh issue list` invocation that looks for an existing
// open issue with EXACTLY title — split out so the argv itself is testable
// with no network, no gh binary and no GitHub remote.
func searchArgv(title string) []string {
	return []string{
		"issue", "list", "--state", "open",
		"--search", fmt.Sprintf("%q in:title", title),
		"--json", "number,title",
	}
}

// commentArgv is the `gh issue comment` invocation an already-open issue gets
// updated with, split out for the same reason as searchArgv.
func commentArgv(number int, body string) []string {
	return []string{"issue", "comment", strconv.Itoa(number), "--body", body}
}

// searchResult is one row of `gh issue list --json number,title`.
type searchResult struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// findExactOpenIssue parses a `gh issue list --json number,title` payload and
// returns the number of the row whose title matches exactly — the search API
// itself is fuzzy, so a substring hit is not good enough to fold two
// different tests' issues together.
func findExactOpenIssue(payload, title string) (number int, found bool, err error) {
	var rows []searchResult
	if err := json.Unmarshal([]byte(payload), &rows); err != nil {
		return 0, false, fmt.Errorf("parsing gh issue list output: %w", err)
	}
	for _, r := range rows {
		if r.Title == title {
			return r.Number, true, nil
		}
	}
	return 0, false, nil
}

// File opens a new issue for f, or — if an open issue with the same title
// already exists — adds a comment carrying this run's seed and excerpt
// instead of filing a duplicate. Returns the issue URL when one was created;
// an update has no fresh URL to report, so the caller is told via updated.
func (fl Filer) File(f Failure) (url string, updated bool, err error) {
	if !suite.GhAvailable() {
		return "", false, fmt.Errorf("gh is not on PATH")
	}
	if !suite.HasGitHubRemote(fl.Repo) {
		return "", false, fmt.Errorf("%s has no GitHub remote", fl.Repo)
	}
	title := TitleFor(f)
	out, err := suite.RunGh(fl.Repo, searchArgv(title)...)
	if err != nil {
		return "", false, fmt.Errorf("searching for an existing issue: %w", err)
	}
	number, found, err := findExactOpenIssue(out, title)
	if err != nil {
		return "", false, err
	}
	if found {
		if _, err := suite.RunGh(fl.Repo, commentArgv(number, BodyFor(f, fl.RunURL))...); err != nil {
			return "", false, fmt.Errorf("commenting on #%d: %w", number, err)
		}
		return "", true, nil
	}
	url, _, err = tdd.OpenIssue(tdd.IssueOptions{
		Repo:          fl.Repo,
		Title:         title,
		Body:          BodyFor(f, fl.RunURL),
		Labels:        []string{fl.Label},
		AllowNewLabel: fl.NewLabel,
	})
	if err != nil {
		return "", false, fmt.Errorf("opening a new issue: %w", err)
	}
	return url, false, nil
}
