package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// premergeRoutineSeam is called once, naming the routine, every time the
// merge gate actually runs Mechanical. Tests substitute it to prove that
// "premerge" and its "premergecommit" alias reach the exact same code path
// rather than two copies that happen to agree.
var premergeRoutineSeam = func(routine string) {}

// runGateMergeHook dispatches precommit, premerge (and its "premergecommit"
// alias, retiring next release) and prepush — the git hook subcommands: no
// stdin from the hook process, exit non-zero to block.
func runGateMergeHook(name string, stderr io.Writer) int {
	// prepush is a mechanical no-op: the tdd gate is mechanical-only and
	// adversarial review lives in the separate reviewer agent, not this
	// binary. It NEVER blocks. We keep the subcommand so a pre-push shim
	// present on a box exits cleanly.
	if name == "prepush" {
		fmt.Fprintln(stderr, "gate prepush: mechanical-only, no-op")
		return 0
	}
	isMerge := name == "premergecommit" || name == "premerge"
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0 // not in a git repo — nothing to gate
	}
	// The merge gate runs ONLY the mechanical stage: a git merge never fires
	// pre-commit, so nothing else has proven the COMBINED tree still
	// compiles and passes — fail-first and the anti-cheat scan are both
	// judgments about how a change was AUTHORED, already settled by
	// precommit on the commits being merged.
	defer tdd.SetPrecommitLockWait(precommitLockWait())()
	var res tdd.GateResult
	if isMerge {
		premergeRoutineSeam("runGatePremerge")
		// Mechanical prints "gate premerge:" itself now — every stage
		// function that builds a message takes the display name, not the
		// pre-rename "premergecommit", so there is nothing to rewrite here.
		// The routine's own internals (gate.log stage tokens and escape
		// fingerprints) still index on "premergecommit" — see
		// premergeLogToken and appendGateLog's remap — because those are
		// read by tooling, never by a human staring at this stderr line.
		res = tdd.Mechanical(root, tdd.RunSuite(precommitTimeout))
		// A rejection HERE is the pre-merge-commit hook blocking an
		// automatic, conflict-free merge — the one case where git still
		// leaves MERGE_HEAD and the merged index in the checkout ("Not
		// committing merge; use 'git commit' to complete the merge."),
		// refusing every OTHER session sharing it until a human runs
		// `git merge --abort`. The marker lets the git-queue shim recognise
		// its own rejection and clean that up automatically. Concluding a
		// CONFLICTED merge fires pre-commit instead (routed to Mechanical
		// internally by Precommit, task A10) and must never reach here —
		// scoping the write to this branch is what keeps that path
		// untouched.
		if res.Blocked {
			tdd.WriteMergeRejectedMarker(root, res.Message)
			// Two gates disagreeing about one tree, or a survivor reaching
			// the last gate that could stop it, is the loop's own evidence
			// about a missing stage. Nothing recorded it before; now it
			// records itself, deduped by fingerprint.
			tdd.NoteMergeGateEscape(root, res.Message, stderr)
		}
	} else {
		res = tdd.Precommit(root, tdd.RunSuite(precommitTimeout))
	}
	if !res.Blocked {
		// Stamp the tree a suite actually RAN GREEN on, so the post-commit
		// hook can put the gate note on the commit and CI can tell a red on
		// a proven tip from a red on an ungated one. A gate that allowed the
		// commit because there was nothing to test has proven nothing and
		// stamps nothing.
		//
		// The merge gate stamps too (#749): its suites are the ones that ran
		// on the merged tree, and the stamp is keyed on that tree, so a
		// commit-msg judging an amend that leaves the tree unchanged reads
		// the merge's own verdict instead of whichever hook ran last. An
		// automatic merge fires no post-commit, so the stamp outlives the
		// merge until the next commit's post-commit consumes it; it vouches
		// for its own tree and no other.
		tdd.StampGreenSuiteIfProven(root)
	}
	// Surface the note (e.g. a fail-open skip) even when allowing — the gate
	// is never silent about why it did or didn't run.
	if res.Message != "" {
		fmt.Fprintln(stderr, res.Message)
	}
	if res.Blocked {
		return 1
	}
	return 0
}
