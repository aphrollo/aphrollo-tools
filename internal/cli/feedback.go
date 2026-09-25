package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const feedbackUsage = `usage: aphrollo gate feedback "<title>" [--label <name>]... [--body <text>] [--repo <dir>] [--upstream <owner/name>]

Opens one issue against the TOOL's tracker rather than the repo you are
standing in, and prints its URL — the only line on stdout, so the command
pipes.

` + "`gate issue`" + ` files where you are, which is right for your own open points and
wrong for a defect in the gate itself: the issue lands in front of maintainers
who cannot fix it, and the tool's tracker never hears. Use this when the fault
is in the TOOL — a stage that rejected a correct commit, a check that passed
something it should have caught, a hook whose one line was wrong.

The body carries the reporting repo and its tip automatically, because a
report nobody can reproduce is a report nobody can act on. Say what you saw
and paste the verbatim output; provenance is added for you.

The target is ` + "`upstream`" + ` under [workspace.metadata.aphrollo] in Cargo.toml or
[aphrollo] in aphrollo.toml, defaulting to the tool's own tracker. Labels are
NOT checked against your repo's declared themes — they belong to the tracker
being filed into.
`

// runGateFeedback opens one issue against the tool's own tracker. Everything
// it says goes to stderr; stdout carries the URL alone.
func runGateFeedback(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, feedbackUsage)
		return 0
	}
	// The title is positional and may sit on either side of the flags, the
	// same as `gate issue` — a title typed first has to be lifted off before
	// parsing, one typed last comes back as fs.Args().
	var leading []string
	for len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		leading = append(leading, args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var labels stringList
	fs.Var(&labels, "label", "a label on the upstream tracker (repeatable)")
	var (
		body     = fs.String("body", "", "what you saw — paste the verbatim output")
		repo     = fs.String("repo", ".", "the checkout the report is coming FROM")
		upstream = fs.String("upstream", "", "override the tracker to file into, as owner/name")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	title := strings.Join(append(leading, fs.Args()...), " ")
	if strings.TrimSpace(title) == "" {
		fmt.Fprintf(stderr, "aphrollo gate feedback: a report needs a title\n\n%s", feedbackUsage)
		return 2
	}

	root := tdd.RepoRoot(*repo)
	target := *upstream
	if target == "" {
		target = tdd.UpstreamRepo(root)
	}

	if line := undercoverTextRefusal(root, [2]string{"report title", title}, [2]string{"report body", *body}); line != "" {
		fmt.Fprintf(stderr, "aphrollo gate feedback: %s\n", line)
		return 1
	}
	url, _, err := tdd.OpenIssue(tdd.IssueOptions{
		Repo:       root,
		TargetRepo: target,
		Title:      strings.TrimSpace(title),
		Body:       tdd.FeedbackBody(root, *body),
		Labels:     labels,
		// The tracker being filed into declares its own themes, and this
		// repo cannot read them.
		AllowNewLabel: true,
	})
	if err != nil {
		if errors.Is(err, tdd.ErrNoIssueTarget) {
			fmt.Fprintf(stderr, "aphrollo gate feedback: gh is not on PATH, so there is no way to reach %s — install gh, or open the issue by hand\n", target)
			return 1
		}
		fmt.Fprintf(stderr, "aphrollo gate feedback: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, url)
	return 0
}
