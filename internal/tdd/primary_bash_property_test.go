package tdd

import (
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

// cpCommand is the smallest command bashWriteTargets recognizes: `cp` treats
// its last operand as the write target.
func cpCommand(dest string) string {
	return "cp source.txt " + filepath.ToSlash(dest)
}

func subPath(rt *rapid.T, base string) string {
	seg := safePathSegment(rt, "sub")
	return filepath.Join(base, seg+".txt")
}

// TestPrimaryCheckoutBash_WorktreeWriteNeverDeniedFromPrimary and its sibling
// below are the reverse-case property the primary-checkout Bash rule needs,
// over bashPrimaryDecision — the real classifier PrimaryCheckoutDecision's
// Bash branch calls, judging EACH resolved write target against ITS OWN
// directory (see primary.go) rather than a single fixed root. Real git
// fixtures (primaryRepo), not mocked path strings: PrimaryMergeOnly shells
// out to git, so only an actually-initialised repo answers it at all.

// TestPrimaryCheckoutBash_WorktreeWriteNeverDeniedFromPrimary: a Bash call
// made with the primary checkout as cwd, that writes only inside the LINKED
// worktree, must never be blocked — the rule protects the primary checkout's
// own tree, not every path a command happens to mention.
func TestPrimaryCheckoutBash_WorktreeWriteNeverDeniedFromPrimary(t *testing.T) {
	primary, linked := primaryRepo(t)
	rapid.Check(t, func(rt *rapid.T) {
		target := subPath(rt, linked)
		if d := bashPrimaryDecision(primary, cpCommand(target)); d.Action == Block {
			rt.Fatalf("a write to %q (under the linked worktree) was denied as if it wrote into the primary %q: %+v", target, primary, d)
		}
	})
}

// TestPrimaryCheckoutBash_PrimaryWriteNeverAllowedFromWorktree: a Bash call
// made with the WORKTREE as cwd, that writes into the primary checkout's own
// path (an absolute escape), must still be denied — bashPrimaryDecision
// judges the RESOLVED target's own directory, not the caller's cwd.
func TestPrimaryCheckoutBash_PrimaryWriteNeverAllowedFromWorktree(t *testing.T) {
	primary, linked := primaryRepo(t)
	rapid.Check(t, func(rt *rapid.T) {
		target := subPath(rt, primary)
		if d := bashPrimaryDecision(linked, cpCommand(target)); d.Action != Block {
			rt.Fatalf("a write to %q (under the primary %q) was allowed from worktree cwd %q: %+v", target, primary, linked, d)
		}
	})
}
