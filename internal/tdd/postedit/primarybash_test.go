package postedit

import (
	"path/filepath"
	"testing"
)

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

// countingProbe wraps the question the wall asks of a directory and returns what
// it has been asked since the last read.
func countingProbe(t *testing.T) func() []string {
	t.Helper()
	var asked []string
	old := primaryProbe
	primaryProbe = func(dir string) (string, bool) {
		asked = append(asked, dir)
		return old(dir)
	}
	t.Cleanup(func() { primaryProbe = old })
	return func() []string { got := asked; asked = nil; return got }
}

// PrimaryLanding says where a call would land for the hook that records what the
// wall did. It must cost nothing the hook was not already paying: a blocked call
// is answered from what the wall resolved, and a waived call, which the wall never
// looked at, is resolved once per directory with no git process.
func TestPrimaryLanding_ResolvesOnceAndSpawnsNoGit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(PrimaryEditsEnv, "")
	primary, linked := primaryRepo(t)
	spawns := countGitSpawns(t)
	asked := countingProbe(t)
	p := filepath.ToSlash(primary)
	writes3 := bashPayload(t, "pl1", linked, "echo a > "+p+"/a.txt; echo b > "+p+"/b.txt; echo c > "+p+"/c.txt")

	// Blocked: the wall resolves the first target; the landing reuses it.
	if d := PrimaryCheckoutDecision(writes3); d.Action != Block {
		t.Fatalf("setup: the wall should block writes into the primary checkout, got %+v", d)
	}
	asked()
	spawns()
	if got := PrimaryLanding(writes3); got == "" || filepath.Clean(got) != filepath.Clean(primary) {
		t.Errorf("PrimaryLanding after a block = %q, want the primary root %q", got, primary)
	}
	if n := asked(); len(n) != 0 {
		t.Errorf("PrimaryLanding after a block asked %v again, want it answered from the wall's own resolution", n)
	}
	if c := spawns(); len(c) != 0 {
		t.Errorf("PrimaryLanding after a block spawned git: %v", c)
	}

	// Waived: three writes into one directory are one question, and no git process.
	t.Setenv(PrimaryEditsEnv, "1")
	PrimaryCheckoutDecision(writes3)
	asked()
	spawns()
	if got := PrimaryLanding(writes3); got == "" || filepath.Clean(got) != filepath.Clean(primary) {
		t.Errorf("PrimaryLanding of a waived call = %q, want the primary root %q", got, primary)
	}
	if n := asked(); len(n) != 1 {
		t.Errorf("a waived call with three writes into one directory asked %v, want exactly one question", n)
	}
	if c := spawns(); len(c) != 0 {
		t.Errorf("PrimaryLanding of a waived call spawned git: %v", c)
	}

	// Not waived and not blocked: it lands nowhere and asks nothing.
	t.Setenv(PrimaryEditsEnv, "")
	inLane := editPayload(t, "Edit", filepath.Join(linked, "main.go"), "pl2")
	PrimaryCheckoutDecision(inLane)
	asked()
	if got := PrimaryLanding(inLane); got != "" {
		t.Errorf("PrimaryLanding of a write into a lane = %q, want none", got)
	}
	if n := asked(); len(n) != 0 {
		t.Errorf("a call that is not waived asked %v of the landing", n)
	}
}
