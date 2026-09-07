package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const escapeUsage = `usage: aphrollo gate escape <subcommand>

Subcommands:
  record "<reason>"   Record a red that arrived after a local green, and open its
                      issue (--kind escape|false-positive, --from-ci <job>,
                      --evidence <text>, --repo <dir>, --label <theme>,
                      --check <stage|law>). A themed defect needs --check: an
                      escape is a claim that some check could have caught it,
                      and a plain defect belongs in aphrollo issue
  sync                Open issues for every record that has none yet, and mark
                      closed the ones GitHub already closed
  list                Print the open records (--all for closed ones too)
  verify-closure <pr> Refuse a PR that closes an escape without changing a check
  check-closes <pr>   Warn on a bare "#123" mention with no closing keyword and
                      error on a comma list after one ("closes #A, #B" -- GitHub
                      honours only #A)

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
		fs := flag.NewFlagSet("list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		all := fs.Bool("all", false, "print closed records too, not just open ones")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		tdd.ListEscapes(stdout, *all)
		return 0
	case "verify-closure":
		return runEscapeVerifyClosure(args[1:], stdout, stderr)
	case "check-closes":
		return runEscapeCheckCloses(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo gate escape: unknown subcommand %q\n\n%s", args[0], escapeUsage)
		return 2
	}
}

func runEscapeRecord(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var labels stringList
	fs.Var(&labels, "label", "a project theme label the issue also carries (repeatable)")
	var (
		kind     = fs.String("kind", tdd.EscapeKind, "escape (the gate missed it) or false-positive (the gate refused correct work)")
		fromCI   = fs.String("from-ci", "", "the CI job that caught it")
		evidence = fs.String("evidence", "", "the failing output, in one paste")
		repo     = fs.String("repo", ".", "the checkout whose GitHub remote the issue is opened against")
		check    = fs.String("check", "", "the stage or law that could have caught it — required for a themed escape")
		newLabel = fs.Bool("new-label", false, "admit a theme label the repo has not declared")
	)
	// Flags are read wherever they sit, not just before the reason. `flag`
	// stops parsing at the first non-flag argument, and the reason IS one --
	// so `record "..." --kind false-positive` folded the flag text into the
	// reason, which is the issue TITLE, and recorded the default kind anyway.
	flags, positional := splitFlags(fs, args)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	reason := strings.Join(positional, " ")
	root := tdd.RepoRoot(*repo)
	if root == "" {
		root = *repo
	}

	// An ESCAPE is a claim about the gate: some check could have caught this
	// and did not. A themed defect that names no such check is not that claim
	// — it is a plain defect, and a plain defect is an issue, not evidence
	// about a missing stage. A false positive is the other direction (a check
	// refusing correct work), so it owes no such name.
	if len(labels) > 0 && *kind != tdd.FalsePositiveKind && strings.TrimSpace(*check) == "" {
		fmt.Fprintf(stderr, "aphrollo gate escape record: a themed defect is an escape only when a check could have caught it — pass --check <stage|law>, or open it as a plain defect with `aphrollo issue %q --label %s`\n",
			reason, strings.Join(labels, " --label "))
		return 2
	}
	if err := tdd.CheckIssueLabels(root, labels, *newLabel); err != nil {
		fmt.Fprintf(stderr, "aphrollo gate escape record: %v\n", err)
		return 2
	}

	// A CI job's report is judged before it is recorded: the tip has to carry
	// the local gate's green trailer, or the failure says nothing about the
	// gate. Exit 0 either way — the recorder never fails the job it reports on.
	if *fromCI != "" {
		ev := *evidence
		if ev == "" {
			ev = reason
		}
		tdd.RecordCIEscape(tdd.CIEscapeOptions{
			Repo:     root,
			Job:      *fromCI,
			Reason:   reason,
			Evidence: ev,
			Labels:   labels,
			Check:    *check,
		}, stdout)
		return 0
	}

	r, err := tdd.RecordEscape(tdd.EscapeOptions{
		Reason:   reason,
		Kind:     *kind,
		FromCI:   *fromCI,
		Evidence: *evidence,
		Repo:     root,
		Labels:   labels,
		Check:    *check,
	}, stderr)
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
	root := tdd.RepoRoot(*repo)
	n, err := tdd.SyncEscapes(root, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate escape: %v\n", err)
		return 1
	}
	// A demotion candidate is a false positive the log noticed by itself, and
	// this is the one verb that reaches GitHub deliberately. `gate stats`
	// names the candidates but stays a read-only report.
	n += recordDemoteCandidates(root, stdout)
	// An override is the loop pointing the other way: a check that gets
	// switched off or talked past is a false-positive candidate. They are
	// opened HERE rather than at the moment of the override, because one
	// annoyed session is not evidence and an issue mid-session is noise.
	n += recordOverrideCandidates(root, stdout)
	fmt.Fprintf(stdout, "opened %d issue(s)\n", n)
	return 0
}

// recordDemoteCandidates opens the false-positive issue for every check whose
// refusals rose in each of the last two weeks and does not have one open
// already. A log it cannot read is no reason to fail the sync that already
// succeeded, so it simply records nothing.
func recordDemoteCandidates(root string, stdout io.Writer) int {
	path := tdd.GateLogPath()
	if path == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	return tdd.RecordDemoteCandidates(root, tdd.DemoteCandidates(f, time.Now().UTC()), stdout)
}

// recordOverrideCandidates opens the false-positive issue for every check the
// log shows a session going around inside the window. Same fail-quiet rule as
// the demote scan: a log it cannot read is no reason to fail a sync that has
// already succeeded.
func recordOverrideCandidates(root string, stdout io.Writer) int {
	path := tdd.GateLogPath()
	if path == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	return tdd.RecordOverrideCandidates(root, tdd.OverrideCandidates(f, time.Now().UTC()), stdout)
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

func runEscapeCheckCloses(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check-closes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "the checkout the PR belongs to")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "aphrollo gate escape check-closes: one PR number, please")
		return 2
	}
	ok, err := tdd.CheckPRCloses(tdd.RepoRoot(*repo), fs.Arg(0), stdout)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate escape: %v\n", err)
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}

// splitFlags separates fs's flags from the positional arguments, so a flag
// written after a positional is still a flag. It asks fs itself which names
// take a following value, rather than guessing: `--kind false-positive` is
// two argv entries and the second one is not a positional.
//
// An unknown `-name` is passed through as a flag token so fs.Parse reports it
// in its own words -- silently treating it as reason text is exactly the bug
// this replaces.
func splitFlags(fs *flag.FlagSet, args []string) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			return flags, positional
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // --name=value carries its own value
		}
		if !flagTakesValue(fs, name) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positional
}

// flagTakesValue reports whether the named flag consumes the argument after
// it. An unknown name does not: it is about to be rejected by Parse, and
// eating the next argument would take a word of the reason with it.
func flagTakesValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !b.IsBoolFlag()
}
