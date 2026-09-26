package precommit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// A declared command is opaque argv: the gate cannot read diagnostics out of
// its output the way it reads tsc's or eslint's. A command declared with
// baseline = "lines" is judged by its output lines instead. When it fails on
// the staged tree it runs once more on HEAD's tree, in the detached worktree
// the npm baseline uses, and the commit passes only when every line of the
// staged run was printed by HEAD's run too. Each line is compared with its
// own checkout's path taken out and its trailing whitespace trimmed, and
// nothing else, so the same error from the two checkouts is one line. When
// HEAD's run cannot be made the whole failure is held against the commit.

// maxQuotedLines is how many new lines a refusal quotes; the rest are
// counted.
const maxQuotedLines = 20

// declaredLinesStage runs r in root and judges a failure against HEAD's
// output.
func declaredLinesStage(gateName, repoRoot, root string, r Runner, run SuiteRunner) GateResult {
	res := run(r, root)
	ran := func(Runner, string) SuiteResult { return res }
	if res.Passed || res.TimedOut {
		return goCheckStage(gateName, "declared", root, r, ran)
	}
	fresh, err := newLinesSinceHead(repoRoot, root, r, res.Output, run)
	if err != nil {
		blocked := goCheckStage(gateName, "declared", root, r, ran)
		blocked.Message += fmt.Sprintf("no baseline at HEAD (%v), so the whole failure is held against this commit.\n", err)
		return blocked
	}
	if len(fresh) == 0 {
		fmt.Fprintf(os.Stderr, "[declared] gate %s: %s in %s → failed, but printed no line HEAD's run did not; not held against this commit\n",
			gateName, cmdString(r), root)
		AppendGateLog(gateName, root, cmdString(r), "declared-head-only", res.Duration)
		return verdictFor(gateName, "declared", root, cmdString(r), stageOutcome{Kind: outcomePass})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TDD quality: declared command printed %d line(s) its run on HEAD did not, in %s — fix before committing.\n", len(fresh), root)
	fmt.Fprintf(&b, "command: %s\n", cmdString(r))
	for i, l := range fresh {
		if i == maxQuotedLines {
			break
		}
		b.WriteString(l + "\n")
	}
	if len(fresh) > maxQuotedLines {
		fmt.Fprintf(&b, "... and %d more\n", len(fresh)-maxQuotedLines)
	}
	return verdictFor(gateName, "declared", root, cmdString(r), stageOutcome{Kind: outcomeFail, Result: res, Message: b.String()})
}

// newLinesSinceHead runs r in root's place in HEAD's tree and is the lines
// of out it did not print. The tree sits under root's node_modules when root
// has one, so a declared npx finds root's packages, and in the system temp
// dir otherwise.
func newLinesSinceHead(repoRoot, root string, r Runner, out string, run SuiteRunner) ([]string, error) {
	// root is repoRoot or below it, both from the same walk.
	rel, _ := filepath.Rel(repoRoot, root)
	parent := filepath.Join(root, "node_modules")
	if _, err := os.Stat(parent); err != nil {
		parent = ""
	}
	var fresh []string
	err := atHead(repoRoot, parent, func(base string) error {
		res := run(r, filepath.Join(base, rel))
		if res.TimedOut {
			return fmt.Errorf("the run at HEAD did not finish in %.0fs", res.Duration.Seconds())
		}
		fresh = newOutputLines(out, repoRoot, res.Output, base)
		return nil
	})
	return fresh, err
}

// newOutputLines is the lines of now that head does not have, each run's
// lines read by outputLines against its own checkout.
func newOutputLines(now, nowCheckout, head, headCheckout string) []string {
	had := map[string]bool{}
	for _, l := range outputLines(head, headCheckout) {
		had[l] = true
	}
	var fresh []string
	for _, l := range outputLines(now, nowCheckout) {
		if !had[l] {
			fresh = append(fresh, l)
		}
	}
	return fresh
}

// outputLines is out's lines with checkout's path taken out and trailing
// whitespace trimmed.
func outputLines(out, checkout string) []string {
	var lines []string
	for l := range strings.Lines(out) {
		lines = append(lines, strings.TrimRightFunc(strings.ReplaceAll(l, checkout, ""), unicode.IsSpace))
	}
	return lines
}
