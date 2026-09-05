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

// hooksBypassDoor reports whether this invocation skips the pre-commit or
// pre-merge-commit hook by any of the three doors above. rest is the
// CLASSIFICATION form (alias-resolved, per resolveAlias), matching how
// primaryRefusedVerb and gitLockScopeFor are already fed.
func hooksBypassDoor(prefix, rest []string) bool {
	return bypassesHooksVerb(rest) || hooksPathOverridden(prefix)
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
