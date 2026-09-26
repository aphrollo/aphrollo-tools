package failfirst

import (
	"path"
	"path/filepath"
	"strings"
)

// failFirstTestNotReached is the verdict for a proof run that failed before
// it reached the staged tests (#898). It is inconclusive, like the other
// proofs that measured nothing, and says why in its own word so `gate
// stats` never counts it as a red.
const failFirstTestNotReached = "inconclusive (test-not-reached)"

// failureReachedTests reports whether a failed proof run got as far as the
// staged tests: it names a failing test, or it mentions one of the staged
// test files — a compile or import error located in the test is exactly the
// red a test-first commit is expected to show at HEAD. A failure that does
// neither is the runner failing on its own account: npx refusing to fetch a
// tool the worktree has no node_modules for, a config that would not load.
// A line echoing the proof's own arguments is skipped before the file scan:
// npm prints the command it could not spawn (`npm error command sh -c vitest
// related <test> --run`), and that mention of the test is the invocation,
// not the test running. tests are root-relative; args is the runner's argv
// after the program.
func failureReachedTests(output string, tests, args []string) bool {
	if len(ExtractFailingTests(output)) > 0 {
		return true
	}
	invocation := strings.Join(args, " ")
	for line := range strings.Lines(output) {
		if invocation != "" && strings.Contains(line, invocation) {
			continue
		}
		for _, t := range tests {
			if strings.Contains(line, path.Base(filepath.ToSlash(t))) {
				return true
			}
		}
	}
	return false
}

// notReachedNote is the line under the verdict: what the run printed first,
// and where the rest of it is.
func notReachedNote(output string) string {
	first := ""
	for line := range strings.Lines(output) {
		if first = strings.TrimSpace(line); first != "" {
			break
		}
	}
	return "  the run at HEAD failed before it reached the staged tests, so it proved nothing either way: " +
		first + "\n  `aphrollo gate output` shows the whole run"
}
