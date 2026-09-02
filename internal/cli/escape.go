package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const escapeUsage = `usage: aphrollo gate escape <subcommand>

Subcommands:
  record "<reason>"   Record a red that arrived after a local green, and open its
                      issue (--kind escape|false-positive, --from-ci <job>,
                      --evidence <text>, --repo <dir>)
  sync                Open issues for every record that has none yet
  list                Print the open records
  verify-closure <pr> Refuse a PR that closes an escape without changing a check

An ESCAPE is the gate's only direct evidence about what it is missing: CI red
after a local green, a merge gate refusing what precommit allowed, a surviving
mutant, a playtest defect some check could have seen. It is closed by a law or
a stage named in the fix, never by a sentence in a document — which is what
verify-closure enforces, as a CI job on the PR.
`

func runGateEscape(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w, code := stderr, 2
		if len(args) > 0 {
			w, code = stdout, 0
		}
		fmt.Fprint(w, escapeUsage)
		return code
	}
	switch args[0] {
	case "record":
		return runEscapeRecord(args[1:], stdout, stderr)
	case "sync":
		return runEscapeSync(args[1:], stdout, stderr)
	case "list":
		tdd.ListEscapes(stdout)
		return 0
	case "verify-closure":
		return runEscapeVerifyClosure(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo gate escape: unknown subcommand %q\n\n%s", args[0], escapeUsage)
		return 2
	}
}

func runEscapeRecord(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		kind     = fs.String("kind", tdd.EscapeKind, "escape (the gate missed it) or false-positive (the gate refused correct work)")
		fromCI   = fs.String("from-ci", "", "the CI job that caught it")
		evidence = fs.String("evidence", "", "the failing output, in one paste")
		repo     = fs.String("repo", ".", "the checkout whose GitHub remote the issue is opened against")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	reason := ""
	for _, a := range fs.Args() {
		if reason != "" {
			reason += " "
		}
		reason += a
	}

	r, err := tdd.RecordEscape(tdd.EscapeOptions{
		Reason:   reason,
		Kind:     *kind,
		FromCI:   *fromCI,
		Evidence: *evidence,
		Repo:     tdd.RepoRoot(*repo),
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate escape: %v\n", err)
		return 2
	}
	if r.Issue != "" {
		fmt.Fprintf(stdout, "recorded %s and opened %s\n", r.ID, r.Issue)
		return 0
	}
	fmt.Fprintf(stdout, "recorded %s (no issue opened — run `aphrollo gate escape sync` where gh can reach the remote)\n", r.ID)
	return 0
}

func runEscapeSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "the checkout whose GitHub remote the issues are opened against")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	n, err := tdd.SyncEscapes(tdd.RepoRoot(*repo), stdout)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate escape: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "opened %d issue(s)\n", n)
	return 0
}

func runEscapeVerifyClosure(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-closure", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "the checkout the PR belongs to")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "aphrollo gate escape verify-closure: one PR number, please")
		return 2
	}
	ok, err := tdd.VerifyClosure(tdd.RepoRoot(*repo), fs.Arg(0), stdout)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate escape: %v\n", err)
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}
