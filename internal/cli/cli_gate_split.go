package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const splitCommitUsage = `usage: aphrollo gate split-commit [--apply] [-m <message>]

Split a mixed commit that fail-first would refuse. When the staged tests
already pass at HEAD, they need no implementation, so they go in a commit of
their own ahead of it: this proves that the way the commit gate does, then
lists both commits. Dry run by default; --apply writes the first commit.

--apply commits the staged test files (and the staged data they read) alone,
on top of HEAD, from the index. The rest of the staged change stays staged
for an ordinary git commit. It never touches the working tree, and it runs
no checkout, restore, reset or stash, so no edit can be lost. The first
commit is written without running the commit hooks: its tests were just
proved green at HEAD. -m sets its message; without it a default names the
tests.

Exits 1 when there is nothing to split: the staged tests go RED at HEAD, or
no source is staged beside them.
`

// splitCommitSuite runs the proof's suite. A var so the verb's decisions are
// tested without a toolchain run.
var splitCommitSuite = tdd.RunSuite(precommitTimeout)

// runGateSplitCommit is `aphrollo gate split-commit`: plan the split, and
// with --apply write its first commit.
func runGateSplitCommit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate split-commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, splitCommitUsage) }
	apply := fs.Bool("apply", false, "write the test-only commit (default: show the plan)")
	msg := fs.String("m", "", "message of the test-only commit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	root := tdd.RepoRoot(".")
	if root == "" {
		fmt.Fprintln(stderr, "gate split-commit: not inside a git repository")
		return 1
	}
	plan, err := tdd.PlanSplit(root, splitCommitSuite)
	if err != nil {
		fmt.Fprintf(stderr, "gate split-commit: %v\n", err)
		return 1
	}
	if len(plan.Tests) == 0 {
		fmt.Fprintln(stderr, "gate split-commit: nothing to split: no staged test passes at HEAD without the staged source, so the commit gate does not refuse this change for it")
		return 1
	}
	printSplitPlan(stdout, plan, *msg)
	if !*apply {
		fmt.Fprintln(stdout, "\ndry run: nothing changed. Run again with --apply to write the first commit.")
		return 0
	}
	if why := splitCommitRefusal(root, plan, *msg); why != "" {
		fmt.Fprintf(stderr, "gate split-commit: refused: %s\n", why)
		return 1
	}
	commit, err := tdd.ApplySplit(root, plan, *msg)
	if err != nil {
		fmt.Fprintf(stderr, "gate split-commit: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\ncommitted %s (tests only). The rest is still staged: commit it with git commit.\n", commit)
	return 0
}

// printSplitPlan lists the two commits the split makes.
func printSplitPlan(w io.Writer, plan tdd.SplitPlan, msg string) {
	if strings.TrimSpace(msg) == "" {
		msg = plan.Message
	}
	fmt.Fprintln(w, "commit 1 (tests only, already green at HEAD):")
	for _, f := range plan.Tests {
		fmt.Fprintf(w, "    %s\n", f)
	}
	if len(plan.Names) > 0 {
		fmt.Fprintf(w, "  tests: %s\n", strings.Join(plan.Names, ", "))
	}
	fmt.Fprintf(w, "  message: %s\n", strings.SplitN(msg, "\n", 2)[0])
	fmt.Fprintln(w, "commit 2 (the rest, still staged afterwards):")
	for _, f := range plan.Rest {
		fmt.Fprintf(w, "    %s\n", f)
	}
}

// splitCommitRefusal is the guardrails the plumbing commit would otherwise
// skip, judged the way the git hooks and the git shim judge them: the
// primary-checkout wall (the shim's own decision, for a `commit`), then the
// commit-msg gate over the message the commit will carry. "" means clear.
// The tree guards run inside tdd.ApplySplit.
func splitCommitRefusal(root string, plan tdd.SplitPlan, msg string) string {
	realGit, err := resolveRealGit()
	if err != nil {
		return fmt.Sprintf("cannot resolve git to judge the primary-checkout wall: %v", err)
	}
	if line := primaryRefusalLine(realGit, []string{"commit"}, root, false); line != "" {
		return line
	}
	if strings.TrimSpace(msg) == "" {
		msg = plan.Message
	}
	f, err := os.CreateTemp("", "aphrollo-split-msg-*")
	if err != nil {
		return fmt.Sprintf("cannot write the message to judge it: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(msg + "\n"); err != nil {
		f.Close()
		return fmt.Sprintf("cannot write the message to judge it: %v", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Sprintf("cannot write the message to judge it: %v", err)
	}
	if res := tdd.CommitMsg(root, f.Name()); res.Blocked {
		return res.Message
	}
	return ""
}
