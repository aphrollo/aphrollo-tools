package precommit

import (
	"fmt"
	"strings"
)

// MechanicalLaws is what a merge still owes when CI has already run the
// suites, vet and lint on this very tree: the merged tree's own laws, baseline
// and doc references, which CI's per-PR jobs judge the PR's diff against, not
// the merge. The repo's mutation configuration is checked first, as Mechanical
// does, so a retired key is corrected at the same point on either path.
func MechanicalLaws(repoRoot string) GateResult {
	if _, res := mutantsConfigStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	}
	if res := baselineStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	}
	if res := ratchetStage(premergeDisplayName, repoRoot); res.Blocked {
		return res
	}
	return docsCheckStage(premergeDisplayName, repoRoot)
}

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
	if line, ok := catchUpMergeLine(repoRoot); ok {
		return catchUpMerge(repoRoot, line)
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
		fmt.Fprintln(stderrFor(repoRoot), line)
		notes = append(notes, line)
		return GateResult{Message: strings.Join(notes, "\n")}
	}
	planRoots(premergeDisplayName, repoRoot, groups)
	if res := dirtyTreeStage(premergeDisplayName, repoRoot, groups); res.Blocked {
		return res
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

// catchUpMergeLine is the line a catch-up merge prints, and whether the merge
// in progress is one: trunk being merged INTO a named lane branch (the same
// reading the staged set's base uses). That tree is not what lands on trunk, so
// the suites are CI's to run on the lane after the push.
func catchUpMergeLine(repoRoot string) (string, bool) {
	_, ok := trunkSyncTip(repoRoot)
	branch := strings.TrimSpace(gitOut(repoRoot, "rev-parse", "--abbrev-ref", "HEAD"))
	trunk := strings.TrimPrefix(TrunkBranch(repoRoot), "origin/")
	line := "gate " + premergeDisplayName + ": catch-up merge of " + trunk + " into " +
		branch + " — suites skipped, CI tests the lane"
	return line, ok
}

// catchUpMerge judges a catch-up merge by the cheap stages alone: the
// mutation configuration, the staged-baseline guard, the laws and the doc
// citations. No build, no suite, no build lock.
func catchUpMerge(repoRoot, line string) GateResult {
	AppendGateLog(premergeDisplayName, repoRoot, "catch-up", "catchup-laws", 0)
	notes, refused := treeGuards(premergeDisplayName, repoRoot)
	if refused.Blocked {
		return refused
	}
	var res GateResult
	res.Message = strings.Join(append([]string{line}, notes...), "\n")
	return res
}

// mechanicalRoots is every project root Mechanical groups repoRoot's staged
// set into, from the same grouping Mechanical itself reads. The PR merge gate
// provisions exactly these roots before Mechanical runs. An index that cannot
// be read yields none; Mechanical refuses on that error itself.
func mechanicalRoots(repoRoot string) []string {
	var roots []string
	for _, g := range stagedRootGroups(repoRoot) {
		roots = append(roots, g.Root)
	}
	return roots
}
