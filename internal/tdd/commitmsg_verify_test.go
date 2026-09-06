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
