package cli

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Three doors skip the pre-commit or pre-merge-commit hook without going
// through the documented --no-verify escape the workspace commands log and
// require a reason for: `git commit --no-verify`/`-n`, `git merge
// --no-verify`, and `-c core.hooksPath=...` (or bare `-c core.hooksPath`) in
// the global prefix, which reroutes hook execution away from whatever the
// gate installed regardless of what value it names. None of the three left
// any trace in gate.log before this, so `gate stats` could not see how often
// the hatch was used (issue #314).
//
// The primary checkout refuses all three outright, the same wall
// primaryRefusalLine enforces for a branch-moving verb. A lane ALLOWS them
// -- an agent working a lane legitimately hits a false-positive gate now and
// then -- but every use is logged under the same "override-no-verify"
// verdict token workspace commit's own --no-verify writes, so the two doors
// are counted together.

// bypassesHooksVerb reports whether rest, already alias-resolved, carries a
// verb-level hook bypass. -n is commit's own documented short form of
// --no-verify (git-commit(1)); merge's -n means something else entirely
// (--no-stat, the opposite of --stat), so only commit gets the short form --
// treating merge -n as a hook bypass would refuse an ordinary --no-stat
// merge for no reason.
func bypassesHooksVerb(rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case "commit":
		return hasArg(rest[1:], "--no-verify") || hasArg(rest[1:], "-n")
	case "merge":
		return hasArg(rest[1:], "--no-verify")
	}
	return false
}

// hooksPathOverridden reports whether prefix (git's own global options,
// collected before the verb by gitGlobalArgs) sets core.hooksPath via -c.
// Git accepts this as one token, `-c core.hooksPath=<value>`, or a bare `-c
// core.hooksPath` (an empty/boolean value) -- either reroutes hook execution
// away from whatever the gate installed, regardless of what the value names.
func hooksPathOverridden(prefix []string) bool {
	for i, a := range prefix {
		if a != "-c" || i+1 >= len(prefix) {
			continue
		}
		key, _, _ := strings.Cut(prefix[i+1], "=")
		if strings.EqualFold(key, "core.hooksPath") {
			return true
		}
	}
	return false
}

// hookRunningVerbs are the git verbs that consult at least one of the six
// hooks core.hooksPath reroutes away from wherever the gate installed them
// (pre-commit, pre-merge-commit, pre-push, prepare-commit-msg, commit-msg,
// post-merge) -- determined empirically against real git 2.53, not guessed:
//
//   - commit: pre-commit, prepare-commit-msg, commit-msg
//   - merge: pre-merge-commit (non-fast-forward), prepare-commit-msg, commit-msg, post-merge
//   - push: pre-push
//   - rebase: prepare-commit-msg (per replayed commit, both the clean and the
//     conflict-and-continue path) -- pre-commit and commit-msg are NOT
//     invoked; the sequencer builds each replayed commit directly, skipping
//     both
//   - cherry-pick, revert: prepare-commit-msg, same sequencer path as
//     rebase -- pre-commit and commit-msg likewise skipped
//
// `am` is deliberately absent: verified against real git, in both the clean
// apply and the conflict-plus-`--continue` path, it runs only its own
// applypatch-msg/pre-applypatch/post-applypatch hooks -- none of the six
// above -- so `-c core.hooksPath` on `git am` bypasses nothing this gate (or
// any repo's own foreign hook of the six kinds) could have run.
//
// Every read-only verb (status, rev-parse, log, diff, fetch, ...) and every
// other mutating one this shim handles (checkout, switch, stash, reset,
// worktree, add) was verified the same way to run none of the six either.
var hookRunningVerbs = map[string]bool{
	"commit":      true,
	"merge":       true,
	"push":        true,
	"rebase":      true,
	"cherry-pick": true,
	"revert":      true,
}

// hooksBypassDoor reports whether this invocation skips the pre-commit or
// pre-merge-commit hook by any of the three doors above. rest is the
// CLASSIFICATION form (alias-resolved, per resolveAlias), matching how
// primaryRefusedVerb and gitLockScopeFor are already fed. The -c
// core.hooksPath door only counts on a verb that actually runs a hook --
// `git status -c core.hooksPath=...` reroutes hook execution for a verb that
// consults no hook at all, so there is nothing to bypass.
func hooksBypassDoor(prefix, rest []string) bool {
	if bypassesHooksVerb(rest) {
		return true
	}
	return len(rest) > 0 && hookRunningVerbs[rest[0]] && hooksPathOverridden(prefix)
}

// hooksBypassRefusalLine returns the primary-checkout refusal for a hooks-
// bypass door, "" when the invocation does not use one, is waived by
// APHROLLO_PRIMARY_EDITS, or this checkout is not the merge-only primary one
// -- a lane's use of the same door is allowed, just logged, by the caller.
func hooksBypassRefusalLine(prefix, rest []string, workDir string) string {
	if !hooksBypassDoor(prefix, rest) || tdd.PrimaryEditsAllowed("") {
		return ""
	}
	root, ok := tdd.PrimaryMergeOnly(workDir)
	if !ok {
		return ""
	}
	return "gate: " + tdd.PrimaryMergeOnlyReason(root) + " -- " + strings.Join(rest, " ") + " bypasses the pre-commit hook, which is refused in the primary checkout"
}
