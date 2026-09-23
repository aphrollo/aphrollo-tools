package precommit

import (
	"fmt"
	"os"
	"strings"
)

// Mechanical runs ONLY the mechanical stage of the commit-time TDD wall,
// grouped by project root exactly like Precommit — but with NO fail-first (a
// fresh test's RED/GREEN belongs to the AUTHORING commit, already proven
// there by Precommit) and NO anti-cheat suppression scan (same reasoning:
// both are judgments about how a change was AUTHORED, not whether the
// resulting combined tree still compiles and passes, which is the only thing
// a merge can meaningfully re-check). Used by the pre-merge-commit gate: two
// branches that each individually passed Precommit can still integrate
// broken — that's what a merge combining them can introduce, and only the
// mechanical stage catches it. A merge whose staged set has nothing to test
// (e.g. a docs-only merge) says so explicitly rather than returning a bare
// empty result indistinguishable from "the gate never ran".
func Mechanical(repoRoot string, run SuiteRunner) GateResult {
	resetSuiteProof()
	var notes []string
	// First, and ahead of the docs-only fast path as well: a repo whose
	// mutation configuration names a retired key believes it is gated and is
	// not, and that correction costs one line to deliver. Everything below
	// this costs a build.
	if _, res := mutantsConfigStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	}
	if fast := StagedFastPath(repoRoot); fast != DiffCode {
		var res GateResult
		if fast == DiffDocsOnly {
			res = docsOnlyFastPath(premergeDisplayName, repoRoot)
		} else {
			res = commentOnlyFastPath(premergeDisplayName, repoRoot)
		}
		if len(notes) > 0 {
			notes = append(notes, res.Message)
			res.Message = strings.Join(notes, "\n")
		}
		return res
	}

	// Same order as Precommit, and for the same reason: a merge carrying only
	// a raised baseline or a law regression must answer for it before the
	// has-code check can wave it through.
	if res := baselineStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	}
	if res := ratchetStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	} else if res.Message != "" {
		notes = append(notes, res.Message)
	}
	if res := docsCheckStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	} else if res.Message != "" {
		notes = append(notes, res.Message)
	}

	groups, err := stagedRootGroupsErr(repoRoot)
	if err != nil {
		return GateResult{Blocked: true, Message: unreadableIndexMessage(premergeDisplayName, err)}
	}
	if len(groups) == 0 {
		line := nothingToTestLine(premergeDisplayName)
		fmt.Fprintln(os.Stderr, line)
		notes = append(notes, line)
		return GateResult{Message: strings.Join(notes, "\n")}
	}
	for _, g := range groups {
		res := gateRoot(premergeDisplayName, repoRoot, g, run, false)
		if res.Blocked {
			return res
		}
		if res.Message != "" {
			notes = append(notes, res.Message)
		}
	}
	// Last, and only once the merged tree has proved itself green. The order
	// is a cost argument in both directions: skipping a suite is cheaper than
	// repeating a mutation run, but a merge whose suite is red must not spend
	// twenty minutes measuring mutants it is never going to land.
	if res := mutantsStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	} else if res.Message != "" {
		notes = append(notes, res.Message)
	}
	return GateResult{Message: strings.Join(notes, "\n")}
}
