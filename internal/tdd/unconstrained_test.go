package tdd

import (
	"strings"
	"testing"
)

// TestPostEdit_GreenUnconstrained pins the advisory that names the hole the
// gate cannot see: a source edit whose related tests all pass, with the SAME
// pass count as the last green, means the new behaviour is covered by exactly
// nothing new. Fail-first never fires (no test was staged), the suite is
// green, and the change ships unconstrained. It is a note, never a block.
func TestPostEdit_GreenUnconstrained(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	green := fakeRun(true, "test result: ok. 7 passed; 0 failed")

	// First edit establishes the baseline: nothing to compare against yet.
	if got := PostEdit(postPayload("Edit", root+"/widget.go"), green); strings.Contains(got, "unconstrained") {
		t.Fatalf("the first green has no previous count to compare with: %s", got)
	}

	got := PostEdit(postPayload("Edit", root+"/widget.go"), green)
	if !strings.Contains(got, "green-unconstrained") {
		t.Fatalf("advisory = %q, want green-unconstrained for a source edit that added no test", got)
	}
	if !strings.Contains(got, "7 passed") || !strings.Contains(got, "mutation") {
		t.Fatalf("advisory = %q, want the pass count and the mutation-proof hint", got)
	}
}

// TestPostEdit_GreenUnconstrained_NotWhenTheTestSetGrew pins the other side:
// a run with MORE passing tests than the last green is exactly the case the
// advisory must stay quiet about — a test came with the change.
func TestPostEdit_GreenUnconstrained_NotWhenTheTestSetGrew(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")

	PostEdit(postPayload("Edit", root+"/widget.go"), fakeRun(true, "test result: ok. 7 passed; 0 failed"))
	got := PostEdit(postPayload("Edit", root+"/widget.go"), fakeRun(true, "test result: ok. 8 passed; 0 failed"))
	if strings.Contains(got, "unconstrained") {
		t.Fatalf("advisory = %q, want a plain green when a test came with the edit", got)
	}
}

// TestPostEdit_GreenUnconstrained_NotForATestEdit pins the scope: editing a
// TEST file is the very thing the advisory asks for, so it must never fire
// there.
func TestPostEdit_GreenUnconstrained_NotForATestEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	green := fakeRun(true, "test result: ok. 7 passed; 0 failed")

	PostEdit(postPayload("Edit", root+"/widget_test.go"), green)
	got := PostEdit(postPayload("Edit", root+"/widget_test.go"), green)
	if strings.Contains(got, "unconstrained") {
		t.Fatalf("advisory = %q, want silence about coverage when the edit IS a test", got)
	}
}

// TestGreenUnconstrained_PersistsAsGreen pins what /tdd status shows: the
// advisory is about coverage, not about failure, so the recorded outcome
// stays green and no streak moves.
func TestGreenUnconstrained_PersistsAsGreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := mkProject(t, "go.mod")
	green := fakeRun(true, "test result: ok. 7 passed; 0 failed")
	PostEdit(postPayload("Edit", root+"/widget.go"), green)
	PostEdit(postPayload("Edit", root+"/widget.go"), green)

	state, _ := loadSession("sess-post")
	if state == nil {
		t.Fatal("no session state")
	}
	ps, ok := state.ByProject[root]
	if !ok {
		t.Fatal("project not recorded")
	}
	if Outcome(ps.Outcome).IsRed() {
		t.Fatalf("outcome = %q, want a green-family outcome", ps.Outcome)
	}
	if ps.TimeoutStreak != 0 {
		t.Fatalf("timeout streak = %d, want an advisory that never touches streaks", ps.TimeoutStreak)
	}
}
