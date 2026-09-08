package tdd

import (
	"strings"
	"testing"
)

// TestCommitMsg_RejectsAVerificationClaimWithNoGreenSuiteForTheTree pins the
// overclaim half of #325: a message that CLAIMS the change was tested must
// be backed by a real green suite run against the exact tree being
// committed, not just the words.
func TestCommitMsg_RejectsAVerificationClaimWithNoGreenSuiteForTheTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	body := "Add the missing configuration constant\n\nVerified locally: all tests pass now.\n"

	got := CommitMsg(root, msgFile(t, body))

	if !got.Blocked {
		t.Fatal("a verification claim with no proven suite for this tree was allowed")
	}
	if !strings.Contains(got.Message, `"no precommit run recorded"`) {
		t.Fatalf("rejection = %q, want it to quote the last actual verdict", got.Message)
	}
}

// TestCommitMsg_AllowsAVerificationClaimWhenTheGreenSuiteStampMatchesTheTree
// pins the other side: the same claim is honest, and must pass, when a suite
// in this pre-commit run actually went green on the tree being committed.
func TestCommitMsg_AllowsAVerificationClaimWhenTheGreenSuiteStampMatchesTheTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampGreenSuite(root)
	body := "Add the missing configuration constant\n\nVerified locally: all tests pass now.\n"

	got := CommitMsg(root, msgFile(t, body))

	if got.Blocked {
		t.Fatalf("a claim backed by a fresh green suite for this exact tree was rejected: %s", got.Message)
	}
}

// claimBody is the shape #591 reports: a body carrying the record the tdd
// skill demands for existing code, which the verification-claim pattern reads
// as a claim.
const claimBody = "Add the missing configuration constant\n\n" +
	"Mutation proof: flipping the sign in Add fails TestAdd; restored. Verified locally.\n"

// TestVerificationClaim_CacheHitOnAGreenTreeIsAccepted is #591: the pre-commit
// suite stage answered "cache-hit", which means the identical worktree state
// is already recorded green — by the post-edit hook, one process earlier. The
// tree is identical by construction (that is what the cache key IS), so the
// earlier green is the same evidence, and refusing it blocks precisely the
// record CLAUDE.md and the tdd skill require in the body.
func TestVerificationClaim_CacheHitOnAGreenTreeIsAccepted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")

	// What the post-edit hook left behind: this exact worktree state, proven
	// green under one exact command.
	runner := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	mechCacheAdd(mechKey(root, worktreeStateHash(root), runner))
	appendGateLog("precommit", root, cmdString(runner), "cache-hit", 0)

	got := CommitMsg(root, msgFile(t, claimBody))

	if got.Blocked {
		t.Fatalf("a cache-hit that resolves to a green run on THIS tree must satisfy the claim: %s", got.Message)
	}
}

// TestVerificationClaim_CacheHitOnATimeoutTreeIsStillRefused is the other
// direction: accepting a cache-hit must mean RESOLVING it, not trusting the
// word. A tree whose only recorded verdict is a timeout has no green in the
// cache to resolve to — only greens are ever cached — so the claim stays
// refused.
func TestVerificationClaim_CacheHitOnATimeoutTreeIsStillRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")

	runner := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	// A green recorded for some OTHER state, and a timeout for this one: the
	// cache holds nothing about the tree being committed.
	mechCacheAdd(mechKey(root, "a-state-this-tree-never-had", runner))
	appendGateLog("precommit", root, cmdString(runner), "timeout", 0)
	appendGateLog("precommit", root, cmdString(runner), "cache-hit", 0)

	got := CommitMsg(root, msgFile(t, claimBody))

	if !got.Blocked {
		t.Fatal("a cache-hit that resolves to no green for this tree is not evidence, and must still be refused")
	}
}
