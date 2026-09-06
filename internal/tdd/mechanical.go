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

// mutationReceiptStage judges the lane's mutation receipt, for workspaces
// that asked for it (`mutation-receipt = true`). nil means "allow": the
// workspace has not opted in, or the receipt covers this tree.
func mutationReceiptStage(repoRoot string) *GateResult {
	// Whichever manifest the repo has: a Cargo workspace declares the opt-in
	// in [workspace.metadata.aphrollo], a Go or Python repo in a root
	// aphrollo.toml. Reading only the first made the key inert in every repo
	// that has no Cargo.toml — aphrollo-tools declared it and merged on
	// nothing at all.
	if !mutationReceiptOptIn(repoRoot) {
		return nil
	}
	// A repo whose proof is measured in CI has no local producer to demand a
	// receipt from: `mutants-local = false` stops the post-commit run, and the
	// receipt the runner writes is signed with the RUNNER's machine key, so
	// this gate could neither find it nor verify it. Refusing anyway would
	// refuse every lane merge forever. The stand-down is logged, so "no
	// receipt was required" never reads as "a receipt was checked".
	if !mutationJudgedLocally(repoRoot) {
		appendGateLog(premergeLogToken, logToken(repoRoot), "receipt", "receipt-measured-in-ci", 0)
		return &GateResult{Message: "mutation receipt not judged here: this repo measures it on the CI runner (mutants-local = false)"}
	}
	// Only the direction that matters. A receipt proves a LANE was measured
	// before it lands on main; a catch-up merge of main INTO a lane proves
	// nothing about the lane, and refusing it drove a builder to squash-merge
	// instead — which polluted the lane's merge-base diff with all of main's
	// changes and made every later mutation run measure them (issue #110).
	if why, catchUp := catchUpMerge(repoRoot); catchUp {
		appendGateLog(premergeLogToken, repoRoot, "receipt", "catchup-merge", 0)
		return &GateResult{Message: "mutation receipt not judged: " + why}
	}
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		// The gate failed on its own inputs, so it says which input: a clean
		// automerge has no MERGE_HEAD yet, and only GIT_REFLOG_ACTION names
		// the branch coming in.
		return blockReceipt(repoRoot, "no-lane-tip", "no lane tip to look a receipt up by (neither .git/MERGE_HEAD nor %s names a merged branch)", reflogActionEnv)
	}
	return checkMutationReceipt(newReceiptContext(repoRoot, tip))
}
