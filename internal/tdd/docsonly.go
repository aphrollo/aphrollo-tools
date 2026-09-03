package tdd

import (
	"fmt"
	"os"
	"strings"
)

// A commit that stages no code has nothing to build and nothing to run: every
// stage past the law scan judges source, and there is none. It used to reach
// those stages anyway and pay for them -- measured on the real tree, a
// Markdown-only commit's ratchet stage took 132.7s and a workflow-only one
// 418.9s, while the same scan by hand takes about 2s over 1921 files. The
// difference was time spent queueing for the machine-wide build slot, which a
// text scan never needed. Prose must not wait behind somebody else's compile.
//
// So a change whose whole staged set is neither Source nor Test takes a fast
// path: the guards that judge the TREE (a hand-raised ceiling, the declared
// laws, the doc citations) still run -- they are milliseconds and they are
// exactly the rules a docs-only commit can break -- and nothing else does. No
// suite, no worktree, no build slot, at any point.

// docsOnly reports whether repoRoot's staged set carries no code at all. An
// EMPTY staged set is not docs-only: there is nothing to say about it, and
// the ordinary path already handles it.
func docsOnly(repoRoot string) bool {
	staged := stagedFiles(repoRoot)
	if len(staged) == 0 {
		return false
	}
	tests, srcs := splitKinds(staged)
	return len(tests) == 0 && len(srcs) == 0
}

// docsOnlyFastPath runs the tree guards and stops. It never takes the build
// lock, because none of the three stages it runs compiles anything.
func docsOnlyFastPath(gateName, repoRoot string) GateResult {
	fmt.Fprintf(os.Stderr, "gate %s: no source or test staged → docs-only fast path (baseline, laws, doc citations; no suite, no build lock)\n", gateName)
	appendGateLog(gateName, repoRoot, "docs-only", "docs-only-fastpath", 0)

	var notes []string
	for _, stage := range []func(string, string) GateResult{baselineStage, ratchetStage, docsCheckStage} {
		res := stage(gateName, repoRoot)
		if res.Blocked {
			return res
		}
		if res.Message != "" {
			notes = append(notes, res.Message)
		}
	}
	// A verdict that says nothing is indistinguishable from a gate that never
	// ran, so the fast path states what it decided not to do.
	line := nothingToTestLine(gateName)
	fmt.Fprintln(os.Stderr, line)
	notes = append(notes, line)
	return GateResult{Message: strings.Join(notes, "\n")}
}

// nothingToTestLine is the one sentence a code-free change gets, in the exact
// wording the merge gate has always used.
func nothingToTestLine(gateName string) string {
	return "gate " + gateName + ": nothing to test (no staged source or test files)"
}
