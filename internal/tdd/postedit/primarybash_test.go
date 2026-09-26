package postedit

import "testing"

// #894: `aphrollo gate allow primary` waives WallPrimary for the whole
// session, but a Bash/PowerShell command still has to reach git as a
// SEPARATE subprocess a moment later, where the git queue shim cannot always
// read this session's identity back out of its own environment.
// PrimaryCheckoutDecision must leave a spent record the shim's
// ConsumePrimaryBashSpent can still consume, scoped to EXACTLY the command's
// git argv.
func TestPrimaryCheckoutDecision_WaivedSessionLeavesASpentRecordTheShimCanConsume(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-primary-bash-spent")
	if _, err := AllowWall(WallPrimary); err != nil {
		t.Fatal(err)
	}

	got := PrimaryCheckoutDecision(bashPayload(t, "s-primary-bash-spent", "/repo", "git commit -a -m docs"))
	if got.Action == Block {
		t.Fatalf("Action = %v, want not Block — the session waived WallPrimary", got.Action)
	}

	argv := []string{"commit", "-a", "-m", "docs"}
	if !ConsumePrimaryBashSpent(argv) {
		t.Fatal("ConsumePrimaryBashSpent(argv) = false, want true — a waived session must leave a matching spent record for the shim")
	}
	if ConsumePrimaryBashSpent(argv) {
		t.Fatal("a second consumption of the same argv must find nothing spent — one record covers one command")
	}
}

// A spent record is scoped to the EXACT argv the hook approved — a different
// git command in the same window must not ride it.
func TestPrimaryCheckoutDecision_WaivedSessionDoesNotSpendADifferentCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-primary-bash-spent-2")
	if _, err := AllowWall(WallPrimary); err != nil {
		t.Fatal(err)
	}

	got := PrimaryCheckoutDecision(bashPayload(t, "s-primary-bash-spent-2", "/repo", "git commit -a -m docs"))
	if got.Action == Block {
		t.Fatalf("Action = %v, want not Block", got.Action)
	}

	if ConsumePrimaryBashSpent([]string{"checkout", "-b", "lane/x"}) {
		t.Fatal("a spent record for `commit -a -m docs` must not authorize an unrelated `checkout -b`")
	}
}

// Without the waiver, PrimaryCheckoutDecision must not record anything for
// the shim to consume — a session that never ran `gate allow primary` gets
// no help from a spent record it never authorized.
func TestPrimaryCheckoutDecision_UnwaivedSessionLeavesNoSpentRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-primary-not-waived")

	PrimaryCheckoutDecision(bashPayload(t, "s-primary-not-waived", "/repo", "git commit -a -m docs"))

	if ConsumePrimaryBashSpent([]string{"commit", "-a", "-m", "docs"}) {
		t.Fatal("an unwaived session must not leave a spent record")
	}
}
