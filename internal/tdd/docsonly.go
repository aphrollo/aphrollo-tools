package tdd

import (
	"fmt"
	"os"
	"strings"
)

// A commit that stages no code has nothing to build and nothing to run: every
// stage past the law scan judges source, and there is none. It used to walk
// the whole ladder anyway -- vet, lint, the compile-coverage check, the
// fail-first worktree, the touched crates' suites -- all of which compile, and
// all of which queue for the machine-wide build slot behind somebody else's
// build. Prose must not wait behind a compile.
//
// So a change whose whole staged set is neither Source nor Test takes a fast
// path: the guards that judge the TREE (a hand-raised ceiling, the declared
// laws, the doc citations) still run -- they are exactly the rules a docs-only
// commit can break, and a raised baseline arrives with no code attached -- and
// nothing else does. What the path guarantees is narrow and worth stating
// exactly: no suite, no worktree, and no build slot taken at any point.
//
// It does not make the law scan itself free. Measured in borld on 2026-09-03,
// 26 laws over 1936 files: 6.8s cold, 0.78s warm. (The 132.7s and 418.9s
// figures that first prompted this path came from a binary whose `cargo
// metadata` call went through the build queue; that verb is read-only and
// passes through unlocked now.)

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
