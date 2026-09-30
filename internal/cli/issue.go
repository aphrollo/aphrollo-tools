package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const issueUsage = `usage: aphrollo issue "<title>" [--label <name>]... [--body <text>] [--repo <dir>] [--new-label] [--dry]

Opens one issue against the repo's GitHub remote and prints its URL — the only
line on stdout, so the command pipes. --dry prints the title, labels and body
and calls no gh, which is also how to preview the undercover check. A label the repo has not declared is
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

// printIssuePreview is what --dry prints in place of calling gh: the title,
// the labels and the body the issue would carry. It is a separate function so
// `issue`, `feedback` and `gate escape record` preview in one shape.
func printIssuePreview(w io.Writer, verb, title string, labels []string, body string) {
	fmt.Fprintf(w, "%s (dry run): nothing opened\n", verb)
	fmt.Fprintf(w, "title: %s\n", title)
	fmt.Fprintf(w, "labels: %s\n", strings.Join(labels, ", "))
	fmt.Fprintf(w, "body:\n%s\n", body)
}

// runGateIssue opens one labelled issue. Everything it says goes to stderr;
// stdout carries the URL alone.
func runGateIssue(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, issueUsage)
		return 0
	}
	// The title is positional and may sit on either side of the flags, or
	// between them: a hand types all three spellings, and discarding a flag
	// that follows the title refuses input that is right there.
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var labels stringList
	fs.Var(&labels, "label", "a label from the repo's declared list (repeatable)")
	var (
		body     = fs.String("body", "", "the issue body")
		repo     = fs.String("repo", ".", "the checkout whose GitHub remote the issue is opened against")
		newLabel = fs.Bool("new-label", false, "admit a label the repo has not declared")
		dry      = fs.Bool("dry", false, "print the title, labels and body and open nothing")
	)
	words, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	title := strings.Join(words, " ")
	if strings.TrimSpace(title) == "" {
		fmt.Fprintf(stderr, "aphrollo issue: an issue needs a title\n\n%s", issueUsage)
		return 2
	}

	if line := undercoverTextRefusal(tdd.RepoRoot(*repo), [2]string{"issue title", title}, [2]string{"issue body", *body}); line != "" {
		fmt.Fprintf(stderr, "aphrollo issue: %s\n", line)
		return 1
	}
	if *dry {
		if err := tdd.CheckIssueLabels(tdd.RepoRoot(*repo), labels, *newLabel); err != nil {
			fmt.Fprintf(stderr, "aphrollo issue: %v\n", err)
			return 1
		}
		printIssuePreview(stdout, "aphrollo issue", strings.TrimSpace(title), labels, *body)
		return 0
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
