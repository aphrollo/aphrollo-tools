package precommit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The call-site guard stage. internal/argvbatch holds two table-keyed guard
// tests over the tool's own source, and CI refuses a function with no row in
// either:
//
//   - callsites_test.go lists every non-test call that hands a slice to a
//     process (exec.Command, or a git or gh helper) and says what bounds it;
//   - relatedsites_test.go lists every non-test function in internal/tdd that
//     names a related-tests verb (vitest's `related`, jest's
//     `--findRelatedTests`), which builds or reads a command line carrying a
//     changed-path list.
//
// The commit gate ran only the packages a commit touched, and argvbatch is not
// one of them when the new call sits in another package, so a missing row
// passed here and failed only in CI (issue #1034 for the exec table, #1079 for
// the related-runner table: a helper naming the verb holds no exec call, so
// the first guard's trigger never saw it).
//
// The stage runs each guard's test, by name, whenever the staged diff adds,
// removes or edits a line that makes a call that guard accounts for in a
// non-test Go file the guard walks, in a repo that carries the guard, or edits
// a function that makes such a call (the table is keyed by function, so moving
// a call into a new function moves its row while the call's own line is only
// diff context), or touches a file the table already names. A commit that
// changes none of these pays for one git diff and a read of the table per
// guard.

const (
	callsiteGuardFile = "internal/argvbatch/callsites_test.go"
	callsiteGuardPkg  = "./internal/argvbatch"
	callsiteGuardTest = "TestExecCallSites_EverySpreadArgumentListIsBatchedOrBounded"
	callsiteGuardName = "callsites"

	relatedGuardFile = "internal/argvbatch/relatedsites_test.go"
	relatedGuardTest = "TestRelatedRunnerSites_EveryBuilderOfARelatedRunIsBounded"
	relatedGuardName = "callsites-related"
	relatedGuardTree = "internal/tdd/"
)

// execCallLine matches a line that names an exec call: exec.Command,
// exec.CommandContext, or a git or gh helper by the naming the guard's own
// wrapper table uses. It is deliberately looser than the guard's AST test (no
// spread check): a false trigger costs one cheap test run, a missed one costs
// a CI round.
var execCallLine = regexp.MustCompile(`\bexec\.Command(?:Context)?\(|\b(?:[gG]it|runGit|probeGit|gh|runGh)\w*\(`)

// relatedVerbLine matches a line that names a related-tests verb as a string
// literal, interpreted or raw, or the jest flag in any text. As loose as
// execCallLine on purpose: a comment that quotes the verb costs one cheap run.
var relatedVerbLine = regexp.MustCompile("[\"`]related[\"`]|--findRelatedTests")

// siteGuard is one table-keyed guard test of internal/argvbatch and what moves
// a row of its table.
type siteGuard struct {
	name  string                // the stage label in the gate's messages and log
	table string                // the table's file; a repo without it does not carry the guard
	test  string                // the guard's test, run by name
	call  *regexp.Regexp        // a source line that makes what the table accounts for
	walks func(rel string) bool // whether the test parses the Go file at rel
}

var (
	execGuard    = siteGuard{callsiteGuardName, callsiteGuardFile, callsiteGuardTest, execCallLine, guardWalks}
	relatedGuard = siteGuard{relatedGuardName, relatedGuardFile, relatedGuardTest, relatedVerbLine, relatedGuardWalks}

	// siteGuards is every guard the stage judges, in the order it runs them.
	siteGuards = []siteGuard{execGuard, relatedGuard}
)

// callsiteGuardStage judges what a commit changes against each guard's table.
// The first guard that blocks ends the stage.
func callsiteGuardStage(gateName, repoRoot string, run SuiteRunner) (res GateResult) {
	for _, g := range siteGuards {
		if !g.needed(repoRoot) {
			continue
		}
		if verdict := g.judge(gateName, repoRoot, run); verdict.Blocked {
			return verdict
		}
	}
	return res
}

// judge runs the guard's test, and blocks on a failure or on a test that no
// longer exists: go test exits 0 when -run matches nothing.
func (g siteGuard) judge(gateName, repoRoot string, run SuiteRunner) GateResult {
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", callsiteGuardPkg, "-run", "^" + g.test + "$"}}
	ran := run(r, repoRoot)
	verdict := goCheckStage(gateName, g.name, repoRoot, r, func(Runner, string) SuiteResult { return ran })
	if verdict.Blocked || !strings.Contains(ran.Output, "no tests to run") {
		return verdict
	}
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"TDD quality: %s ran no test in %s — %s no longer names one, so its table is unchecked.\n", g.name, repoRoot, g.test)}
}

// needed reports whether repoRoot carries the guard and the staged diff moves
// a row of its table.
func (g siteGuard) needed(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(g.table))); err != nil {
		return false
	}
	out, err := git(repoRoot, "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--", "*.go")
	if err != nil {
		// A diff this stage cannot read is not evidence nothing changed:
		// run the check.
		return true
	}
	return g.diffChanges(out) || g.touches(repoRoot, out)
}

// diffChanges reports whether a unified diff adds or removes a line making a
// call the guard accounts for, in a Go file the guard walks.
func (g siteGuard) diffChanges(diff string) bool {
	file, inHunk := "", false
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file, inHunk = "", false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk:
			if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) && g.walks(file) && g.call.MatchString(line[1:]) {
				return true
			}
		case strings.HasPrefix(line, "--- a/"):
			file = strings.TrimSuffix(strings.TrimPrefix(line, "--- a/"), "\t")
		case strings.HasPrefix(line, "+++ b/"):
			file = strings.TrimSuffix(strings.TrimPrefix(line, "+++ b/"), "\t")
		}
	}
	return false
}

// guardWalks reports whether the exec guard parses the file at rel: a non-test
// Go file that is not tool-generated output.
func guardWalks(rel string) bool {
	base := path.Base(rel)
	if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
		return false
	}
	return base != "export.go" && !strings.HasPrefix(base, "deps_") && !strings.HasPrefix(base, "api_")
}

// relatedGuardWalks reports whether the related-runner guard parses the file at
// rel: a non-test Go file under internal/tdd, generated or not, outside the
// scratch and fixture directories it skips.
func relatedGuardWalks(rel string) bool {
	if !strings.HasPrefix(rel, relatedGuardTree) || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	for dir := range strings.SplitSeq(path.Dir(rel), "/") {
		if strings.HasPrefix(dir, "gotmp") || dir == "tddtest" {
			return false
		}
	}
	return true
}
