package mutation

import (
	"fmt"
	"os"
	"strings"
)

// The merge is measured here, on the tree that is about to land, in the
// foreground. What used to sit at this point was a document: the lane's own
// background run wrote a document, and this stage ran seven paperwork checks
// on it before it ever asked whether a mutant had survived. Over three weeks
// that stage refused 150 merges, logged no reason for 141 of them and named a
// survivor in none. The measurement is the whole point, so the measurement is
// what runs.
//
// Two calls, at two different costs. The CONFIG read is first in the whole
// merge gate — a repo that still declares a retired key believes it is gated
// and is not, and telling it so must not cost a suite. The MEASUREMENT is
// last, after the suites: a mutation run takes twenty minutes and a merge
// whose suite is red is going nowhere, so a red suite never pays for one.

// mutantsResult is the one place a mutation verdict becomes the gate's own
// result. The six shapes verdictFor owns — pass, fail, timeout, skipped,
// runner-missing, check-error — do not include "a mutant survived": this
// stage's outcomes carry counts, they are logged with their own
// `mutants-…` tokens by the library that produced them (so `gate stats` can
// answer how many merges the stage refused and for what), and mapping them
// onto "fail" would throw those counts away at the moment they matter.
func mutantsResult(blocked bool, message string) GateResult {
	return GateResult{Blocked: blocked, Message: message}
}

// mutantsConfigStage reads what the repo declares about its mutation run and
// refuses a configuration that addresses a mechanism which no longer exists.
// It is cheap enough to run before every other stage, and that is where it
// belongs: a repo that still declares one of the retired keys is waiting for
// a proof nobody writes, and it must hear so in one line rather than after a
// full suite.
func mutantsConfigStage(displayName, repoRoot string) (MutantsConfig, GateResult) {
	cfg, err := ReadMutantsConfig(repoRoot)
	if err == nil {
		return cfg, mutantsResult(false, "")
	}
	msg := fmt.Sprintf("gate %s: mutants → REJECTED\n  %v", displayName, err)
	fmt.Fprintln(os.Stderr, msg)
	AppendGateLog(displayName, repoRoot, "mutants", "mutants-refused:config", 0)
	return MutantsConfig{}, mutantsResult(true, msg)
}

// mutantsStage is the pre-merge mutation measurement. It passes with an empty
// message when the repo declares nothing, and otherwise hands back the
// verdict's own report verbatim: the surviving mutant first, then the counts,
// then the remedy.
func mutantsStage(displayName, repoRoot string) GateResult {
	cfg, res := mutantsConfigStage(displayName, repoRoot)
	if res.Blocked {
		return res
	}
	if !cfg.AtMerge {
		fmt.Fprintf(os.Stderr, "gate %s: mutants → skipped (this repo declares no mutants-at-merge)\n", displayName)
		AppendGateLog(displayName, repoRoot, "mutants", "mutants-skipped:not-declared", 0)
		return mutantsResult(false, "")
	}
	if reason, token := mutantsStandDown(repoRoot); token != "" {
		fmt.Fprintf(os.Stderr, "gate %s: mutants → skipped (%s)\n", displayName, reason)
		AppendGateLog(displayName, repoRoot, "mutants", "mutants-skipped:"+token, 0)
		return mutantsResult(false, "")
	}
	base, why := mergeMeasureBase(repoRoot)
	if why != "" {
		msg := fmt.Sprintf("gate %s: mutants → REJECTED\n  %s", displayName, why)
		fmt.Fprintln(os.Stderr, msg)
		AppendGateLog(displayName, repoRoot, "mutants", "mutants-refused:no-lane-tip", 0)
		return mutantsResult(true, msg)
	}
	fmt.Fprintf(os.Stderr, "gate %s: mutants → measuring the merged tree against %s\n", displayName, base)
	v, err := MeasureLane(repoRoot, cfg, MeasureOpts{Base: base, Log: os.Stderr})
	if err != nil {
		// The runner never started. That is not "nothing survived": it is a
		// measurement that did not happen, and a merge cannot be let through
		// on one.
		msg := fmt.Sprintf("gate %s: mutants → REJECTED\n  the mutation run could not start: %v", displayName, err)
		fmt.Fprintln(os.Stderr, msg)
		AppendGateLog(displayName, repoRoot, "mutants", "mutants-refused:runner-failed", 0)
		return mutantsResult(true, msg)
	}
	return mutantsResult(v.Refused, v.Message)
}

// mutantsStandDown names what this routine has been handed that is NOT a lane
// landing on trunk, and the token the stand-down is counted under. Empty when
// the merge is one this stage judges.
//
// Two shapes reach the merge routine that a measurement has no business
// judging, and refusing either would break work the gate is not entitled to
// stop:
//
//   - a conflicted cherry-pick or revert, concluded with `git commit`, which
//     routes here through precommitDecide. Neither writes MERGE_HEAD nor a
//     `merge <ref>` reflog action, so there is no incoming tip at all — and
//     nothing is being merged, so there is nothing a measurement would mean.
//   - a CATCH-UP merge of trunk into a lane, which is the push guard's own
//     printed remedy. Nothing lands: the base becomes the fork point and the
//     measured diff becomes the lane's own change PLUS everything trunk did
//     since, so the lane pays for trunk's work and a survivor trunk already
//     accepted refuses the catch-up. The lane is measured when it lands, on
//     trunk, which is the merge that means something.
func mutantsStandDown(repoRoot string) (reason, token string) {
	if ref := mergeInProgressRef(repoRoot); ref != "" && ref != mergeHeadRef {
		return ref + " in progress, which is not a merge", "not-a-merge"
	}
	trunk := TrunkBranch(repoRoot)
	branch := gitOut(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if trunk == "" || branch == "" || branch == "HEAD" || branchIsTrunk(branch, trunk) {
		// No trunk to compare against, a detached HEAD, or HEAD IS trunk:
		// the last is the merge this stage exists for, and the first two are
		// this side's own blind spot, which must not stand a measurement
		// down on its own.
		return "", ""
	}
	return "merging into " + branch + " rather than " + trunk + ", so nothing lands here", "catch-up"
}

// mergeHeadRef is the one in-progress ref that names a merge. The other two
// (CHERRY_PICK_HEAD, REVERT_HEAD) share the hook and share nothing else.
const mergeHeadRef = "MERGE_HEAD"

// mergeMeasureBase is the commit the merged tree is measured against, and why
// it could not be found. At pre-merge-commit HEAD is still trunk and the merge
// result exists only in the index and the working tree, so the base is the
// merge base between HEAD and the tip coming in — which makes the diff the
// runner is handed the whole merged change, not just the half of it HEAD
// already had.
func mergeMeasureBase(repoRoot string) (base, why string) {
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		// The gate failed on its own inputs, so it says which input: a clean
		// automerge has no MERGE_HEAD yet, and only GIT_REFLOG_ACTION names
		// the branch coming in.
		return "", fmt.Sprintf(
			"no lane tip to measure against (neither .git/MERGE_HEAD nor %s names a merged branch)", reflogActionEnv)
	}
	base = strings.TrimSpace(gitOut(repoRoot, "merge-base", "HEAD", tip.Rev))
	if base == "" {
		return "", fmt.Sprintf("no merge base between HEAD and the incoming tip %s (named by %s)", tip.Rev, tip.From)
	}
	return base, ""
}
