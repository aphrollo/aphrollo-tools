package precommit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The call-site guard stage. internal/argvbatch/callsites_test.go lists every
// non-test call that hands a slice to a process (exec.Command, or a git or gh
// helper) and says what bounds it, and CI refuses a call with no row. The
// commit gate ran only the packages a commit touched, and argvbatch is not
// one of them when the new call sits in another package, so the missing row
// passed here and failed only in CI (issue #1034).
//
// The stage runs that one test, by name, whenever the staged diff adds,
// removes or edits a line that makes such a call in a non-test Go file, in a
// repo that carries the guard. Detection reads the staged diff and nothing
// else, so a commit that changes no call pays for one git diff.

const (
	callsiteGuardFile = "internal/argvbatch/callsites_test.go"
	callsiteGuardPkg  = "./internal/argvbatch"
	callsiteGuardTest = "TestExecCallSites_EverySpreadArgumentListIsBatchedOrBounded"
	callsiteGuardName = "callsites"
)

// execCallLine matches a line that names an exec call: exec.Command,
// exec.CommandContext, or a git or gh helper by the naming the guard's own
// wrapper table uses. It is deliberately looser than the guard's AST test (no
// spread check): a false trigger costs one cheap test run, a missed one costs
// a CI round.
var execCallLine = regexp.MustCompile(`\bexec\.Command(?:Context)?\(|\b(?:[gG]it|runGit|probeGit|gh|runGh)\w*\(`)

// callsiteGuardStage judges the exec calls a commit changes against the
// guard's table.
func callsiteGuardStage(gateName, repoRoot string, run SuiteRunner) (res GateResult) {
	if !callsiteGuardNeeded(repoRoot) {
		return res
	}
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", callsiteGuardPkg, "-run", "^" + callsiteGuardTest + "$"}}
	ran := run(r, repoRoot)
	verdict := goCheckStage(gateName, callsiteGuardName, repoRoot, r, func(Runner, string) SuiteResult { return ran })
	if verdict.Blocked || !strings.Contains(ran.Output, "no tests to run") {
		return verdict
	}
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"TDD quality: %s ran no test in %s — %s no longer names one, so the call sites are unchecked.\n", callsiteGuardName, repoRoot, callsiteGuardTest)}
}

// callsiteGuardNeeded reports whether repoRoot carries the guard and the
// staged diff changes an exec call.
func callsiteGuardNeeded(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(callsiteGuardFile))); err != nil {
		return false
	}
	out, err := git(repoRoot, "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--", "*.go")
	if err != nil {
		// A diff this stage cannot read is not evidence no call changed:
		// run the check.
		return true
	}
	return diffChangesExecCall(out)
}

// diffChangesExecCall reports whether a unified diff adds or removes a line
// naming an exec call in a Go file the guard walks: not a test file, not
// generated.
func diffChangesExecCall(diff string) bool {
	file, inHunk := "", false
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file, inHunk = "", false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk:
			if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) && guardWalks(file) && execCallLine.MatchString(line[1:]) {
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

// guardWalks reports whether the guard parses the file at rel: a non-test Go
// file that is not tool-generated output.
func guardWalks(rel string) bool {
	base := path.Base(rel)
	if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
		return false
	}
	return base != "export.go" && !strings.HasPrefix(base, "deps_") && !strings.HasPrefix(base, "api_")
}
