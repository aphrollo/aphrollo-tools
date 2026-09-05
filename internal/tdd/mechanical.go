package tdd

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
	var notes []string
	// The cheapest possible rejection comes first: a lane with no mutation
	// proof is refused before a single suite compiles. But not every non-nil
	// result here IS a rejection — mutationReceiptStage also returns a
	// non-blocking WAIVER for a catch-up merge of main into a lane, and for a
	// repo that measures its proof on the CI runner (`mutants-local =
	// false`). Treating either as a short-circuit used to return before
	// baselineStage, ratchetStage, docsCheckStage or a single suite ran, so
	// the merged tree — lane plus main, the tree nobody has actually tested —
	// went unchecked for exactly the merges the waiver was supposed to still
	// gate (issue #396). Only a Blocked result short-circuits; a waiver's
	// Message rides along in notes the same way a passing stage's does below.
	if res := mutationReceiptStage(repoRoot); res != nil {
		if res.Blocked {
			// The stage already recorded its own receipt-rejected:<reason>
			// token at the call site that actually judged the receipt
			// (issue #376) — logging a second, undifferentiated one here
			// would double-count every rejection under one bucket again,
			// and printing the message here too would state the same
			// paragraph twice: the hook that called this prints what it is
			// given.
			return *res
		}
		// The inner stage already logged which waiver this is
		// (catchup-merge / receipt-measured-in-ci) — logging anything here
		// too would say the same thing twice under a different token.
		if res.Message != "" {
			notes = append(notes, res.Message)
		}
	}
	if docsOnly(repoRoot) {
		res := docsOnlyFastPath(premergeDisplayName, repoRoot)
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

	groups := stagedRootGroups(repoRoot)
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
	return GateResult{Message: strings.Join(notes, "\n")}
}
