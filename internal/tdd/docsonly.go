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
	return buildFreeFastPath(gateName, repoRoot, "docs-only",
		"no source or test staged", false)
}

// commentOnlyFastPath is docsOnlyFastPath's sibling for a staged Rust source
// diff that never left a comment (see commentonly.go): the same three tree
// guards, plus the commit-time anti-cheat suppression scan the pure prose
// case never needed. Prose carries no code file suppressionPolicies applies
// to, but a Source-kind .rs file does, so a directive one of suppress.go's
// linter/type-checker/coverage patterns recognizes, added inside an
// otherwise comment-only edit, must still block here rather than riding
// through on the fast path unmeasured (issue #723's correctness bar: a
// directive a LAW or the suppression scan reads is not exempt just because
// it sits in a comment). Scoped to precommit only -- Mechanical's merge-time
// pass never runs the suppression scan at all, for any staged set, docs-only
// or not (see Mechanical's own doc comment), so this must not add it there.
func commentOnlyFastPath(gateName, repoRoot string) GateResult {
	return buildFreeFastPath(gateName, repoRoot, "comment-only",
		"staged Rust diff changes no token outside a comment", true)
}

// buildFreeFastPath is docsOnlyFastPath and commentOnlyFastPath's shared
// body: run the tree guards (and, when runSuppression is set, the commit-time
// suppression scan) and stop. It never takes the build lock, because none of
// what it runs compiles anything.
func buildFreeFastPath(gateName, repoRoot, kind, reason string, runSuppression bool) GateResult {
	fmt.Fprintf(os.Stderr, "gate %s: %s → %s fast path (baseline, laws, doc citations; no suite, no build lock)\n", gateName, reason, kind)
	appendGateLog(gateName, repoRoot, kind, kind+"-fastpath", 0)

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
	if runSuppression {
		if msg := newSuppression(repoRoot); msg != "" {
			return GateResult{Blocked: true, Message: msg}
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
