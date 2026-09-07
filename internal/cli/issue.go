package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const issueUsage = `usage: aphrollo issue "<title>" [--label <name>]... [--body <text>] [--repo <dir>] [--new-label]

Opens one issue against the repo's GitHub remote and prints its URL — the only
line on stdout, so the command pipes. A label the repo has not declared is
refused with the declared list, because the common case is a typo and a typo
opens a theme nobody ever filters on; --new-label is how a deliberate new theme
goes through. The list is ` + "`issue-labels`" + ` under [workspace.metadata.aphrollo] in
Cargo.toml, or under [aphrollo] in aphrollo.toml.

An open point belongs here rather than in a markdown list: an issue has an
owner, a label and a close event, and a list in a file has none of the three.
A gate MISS — a red that arrived after a local green — is ` + "`gate escape record`" + `
instead, which records it locally as well as opening the issue.
`

// stringList collects a flag given more than once, in order.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// runGateIssue opens one labelled issue. Everything it says goes to stderr;
// stdout carries the URL alone.
func runGateIssue(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, issueUsage)
		return 0
	}
	// The title is positional and may sit on EITHER side of the flags: Go's
	// flag package stops at the first non-flag argument, so a title typed
	// first has to be lifted off before parsing, and one typed last comes
	// back as fs.Args(). Both spellings are how a hand actually types this,
	// and discarding either one refuses a title that is right there.
	var leading []string
	for len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		leading = append(leading, args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var labels stringList
	fs.Var(&labels, "label", "a label from the repo's declared list (repeatable)")
	var (
		body     = fs.String("body", "", "the issue body")
		repo     = fs.String("repo", ".", "the checkout whose GitHub remote the issue is opened against")
		newLabel = fs.Bool("new-label", false, "admit a label the repo has not declared")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	title := strings.Join(append(leading, fs.Args()...), " ")
	if strings.TrimSpace(title) == "" {
		fmt.Fprintf(stderr, "aphrollo issue: an issue needs a title\n\n%s", issueUsage)
		return 2
	}

	url, _, err := tdd.OpenIssue(tdd.IssueOptions{
		Repo:          tdd.RepoRoot(*repo),
		Title:         strings.TrimSpace(title),
		Body:          *body,
		Labels:        labels,
		AllowNewLabel: *newLabel,
	})
	if err != nil {
		// "no gh, or no GitHub remote" is the one failure worth naming in
		// plainer words: there is no local record here to fall back on, so
		// unlike an escape this command has genuinely done nothing.
		if errors.Is(err, tdd.ErrNoIssueTarget) {
			fmt.Fprintf(stderr, "aphrollo issue: nothing to open an issue against — %s has no GitHub remote, or gh is not on PATH\n", *repo)
			return 1
		}
		fmt.Fprintf(stderr, "aphrollo issue: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, url)
	return 0
}
