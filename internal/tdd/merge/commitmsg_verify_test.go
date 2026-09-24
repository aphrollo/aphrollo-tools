package merge

import (
	"path/filepath"
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
	AppendGateLog("precommit", root, cmdString(runner), "cache-hit", 0)

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
	AppendGateLog("precommit", root, cmdString(runner), "timeout", 0)
	AppendGateLog("precommit", root, cmdString(runner), "cache-hit", 0)

	got := CommitMsg(root, msgFile(t, claimBody))

	if !got.Blocked {
		t.Fatal("a cache-hit that resolves to no green for this tree is not evidence, and must still be refused")
	}
}

// A cache hit resolves only to a green that covers the suite the commit owes.
// The gate logs cache-hit for the package's full suite, the tree then moves,
// and the edit hook records a FILTERED green at the new state. That green is a
// fact about one test, not about the package: the full suite never ran on
// the tree being committed, so the claim has nothing to stand on.
func TestVerificationClaim_CacheHitDoesNotResolveToAFilteredGreenOnAMovedTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	full := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	mechCacheAdd(mechKey(root, worktreeStateHash(root), full))
	AppendGateLog("precommit", root, cmdString(full), "cache-hit", 0)

	// The tree moves, and the edit hook proves one test green at the new state.
	write(t, root, "next.go", "package m\n\nconst Next = 2\n")
	gitDo(t, root, "add", ".")
	scoped := Runner{Cmd: "go", Args: []string{"test", "-run", "^TestNext$", "./..."}}
	mechCacheAdd(mechKey(root, worktreeStateHash(root), scoped))

	got := CommitMsg(root, msgFile(t, claimBody))

	if !got.Blocked {
		t.Fatal("a filtered green on the moved tree backed a claim the package's suite never ran for")
	}
}

// A commit whose roots owe no suite (no runner the gate knows) has nothing a
// green could cover, so a cache-hit there backs no claim, whatever the cache
// holds for the tree.
func TestVerificationClaim_CacheHitWithNoOwedSuiteIsRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "README.md", "tool\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "tool.go", "package tool\n")
	gitDo(t, root, "add", ".")
	full := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	mechCacheAdd(mechKey(root, worktreeStateHash(root), full))
	AppendGateLog("precommit", root, cmdString(full), "cache-hit", 0)

	got := CommitMsg(root, msgFile(t, claimBody))

	if !got.Blocked {
		t.Fatal("a cache-hit on a commit that owes no suite backed a verification claim")
	}
}

// The resolution has to key at the same root the SUITE STAGE keyed at, which
// is the PROJECT root stagedRootGroups derived (FindProjectRoot), not the repo
// root. The key carries the root's place in the repo (mechKeyRoot), so a
// prefix built at the repo root is one the cache can never hold for the
// crate, and #591 refuses again in exactly the monorepo shape it came from.
func TestVerificationClaim_CacheHitResolvesAtTheCrateRootNotTheRepoRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	// Committed on its own: a staged workspace manifest opens a second root
	// group at the repo root, which owes a suite of its own.
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/x\"]\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare the workspace")
	write(t, root, filepath.Join("crates", "x", "Cargo.toml"), "[package]\nname = \"x\"\n")
	write(t, root, filepath.Join("crates", "x", "src", "lib.rs"), "pub fn f() -> u32 { 1 }\n")
	gitDo(t, root, "add", ".")
	// Untracked, at the REPO root, written after the add so it stays that way:
	// the scratch file a lane is always carrying.
	write(t, root, "scratch.log", "noise\n")

	crate := filepath.Join(root, "crates", "x")
	if mechKeyPrefix(crate, worktreeStateHash(crate)) == mechKeyPrefix(root, worktreeStateHash(root)) {
		t.Fatal("setup: the crate-root and repo-root key prefixes must differ for this test to mean anything")
	}
	// What the suite stage left behind: a green keyed at the CRATE root.
	runner := Runner{Cmd: "cargo", Args: []string{"test", "-p", "x"}}
	mechCacheAdd(mechKey(crate, worktreeStateHash(crate), runner))
	AppendGateLog("precommit", crate, cmdString(runner), "cache-hit", 0)

	got := CommitMsg(root, msgFile(t, claimBody))

	if got.Blocked {
		t.Fatalf("the cache hit was computed at the crate root and must be resolved there too: %s", got.Message)
	}
}

// #749's other door: the stamp vouches for one commit and the post-commit
// hook consumes it, turning it into the gate note on that commit. Amending
// the message of a commit whose note names its own tree — the index still
// that tree — is the same tree the suite ran green on, and the claim stands
// on the commit's own verdict rather than on whichever hook ran last.
func TestVerificationClaim_AnAmendThatKeepsTheTreeKeepsTheCommitsGreenNote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	var ran []Runner
	if note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, recordRunner(&ran, root)) }); note == "" {
		t.Fatalf("premise broken — the merge-gate run left no note on the commit; runs %+v", ran)
	}

	got := CommitMsg(root, msgFile(t, claimBody))

	if got.Blocked {
		t.Fatalf("an amend over the tree the commit's own note vouches for was refused: %s", got.Message)
	}
}

// The note stays bound to its tree: once the amend stages anything, the
// index is a tree no suite ran on, and the note on HEAD says nothing about it.
func TestVerificationClaim_AnAmendThatChangesTheTreeDoesNotInheritTheNote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	var ran []Runner
	if note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, recordRunner(&ran, root)) }); note == "" {
		t.Fatalf("premise broken — the merge-gate run left no note on the commit; runs %+v", ran)
	}
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 2 }\n")
	gitDo(t, root, "add", ".")

	got := CommitMsg(root, msgFile(t, claimBody))

	if !got.Blocked {
		t.Fatal("a note on HEAD vouched for a staged tree no suite ran on")
	}
}

// Keeping the tree is not enough on its own: the commit being amended must
// carry the note: an amend of a commit no suite ever vouched for claims
// nothing it can stand on.
func TestVerificationClaim_AnAmendOfAnUnprovenCommitIsStillRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "Add X")

	got := CommitMsg(root, msgFile(t, claimBody))

	if !got.Blocked {
		t.Fatal("an amend of a commit carrying no gate note was allowed to claim verification")
	}
}
