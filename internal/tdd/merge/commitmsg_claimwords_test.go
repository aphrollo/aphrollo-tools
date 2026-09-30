package merge

import "testing"

// issue984Body is the body the gate refused in a consuming repo (issue #984):
// it describes what an AI-draft verifier does, and claims nothing about a run.
const issue984Body = "warmup_agent: an unreadable verifier verdict passed the draft unchecked;\n" +
	"it now counts as not verified and sends the draft through the rewrite.\n"

// TestVerificationClaimed_ReadsClaimsAboutRunsAndNothingElse pins what counts
// as a claim that the change was tested: phrases about tests and runs. A
// negation, and a domain word that only shares a stem with one, claim
// nothing (issue #984).
func TestVerificationClaimed_ReadsClaimsAboutRunsAndNothingElse(t *testing.T) {
	claims := []string{
		"Verified locally: all tests pass now.",
		"Mutation proof: flipping the sign in Add fails TestAdd; restored. Verified locally.",
		"Tests pass.",
		"the test suite passes",
		"all green on CI parity",
		"Verified by running go test ./internal/tdd/merge.",
		"verified against the production config",
		"Verified under CI's own flags before merge.",
		"Tested with go test -race ./...",
		"confirmed working on the branch",
		"no regressions",
		"Not everything was covered, but all tests pass.",
		"No, really: verified by hand.",
	}
	for _, c := range claims {
		if !verificationClaimed(c) {
			t.Errorf("not read as a claim: %q", c)
		}
	}
	nonClaims := []string{
		issue984Body,
		"it now counts as not verified",
		"a draft that is unverified goes to the rewrite",
		"never verified by the reviewer, so the draft is rewritten",
		"the change was not tested with the real service",
		"the token is never verified against the old key",
		"the verifier returns a verdict",
		"an unverified draft is not tested with the strict prompt",
		"verified drafts skip the rewrite",
		"",
	}
	for _, c := range nonClaims {
		if verificationClaimed(c) {
			t.Errorf("read as a claim: %q", c)
		}
	}
}

// TestVerificationClaimed_ANegatorReachesTwoWordsBack pins the reach of a
// negation: two words between it and the claim still negate, three do not.
func TestVerificationClaimed_ANegatorReachesTwoWordsBack(t *testing.T) {
	if verificationClaimed("it was not one bit tested with the service") {
		t.Error("a negator two words before the claim did not negate it")
	}
	if !verificationClaimed("it was not one single bit tested with the service") {
		t.Error("a negator three words before the claim negated it")
	}
}

// TestCommitMsg_TheBodyOfIssue984IsNotAVerificationClaim runs the refused
// message through the whole commit-msg gate: with no suite recorded for the
// tree it must pass, where a claim of the same tree is refused.
func TestCommitMsg_TheBodyOfIssue984IsNotAVerificationClaim(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")

	if got := CommitMsg(root, msgFile(t, "Rewrite an unverified draft\n\n"+issue984Body)); got.Blocked {
		t.Fatalf("a body describing verifier behaviour was refused as a claim: %s", got.Message)
	}
	if got := CommitMsg(root, msgFile(t, "Rewrite an unverified draft\n\n"+issue984Body+"Tests pass.\n")); !got.Blocked {
		t.Fatal("a real claim appended to the same body was allowed with no suite behind it")
	}
}
