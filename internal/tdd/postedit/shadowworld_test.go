package postedit

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
)

// The shadow world reads the live ledger and store of the box: an edit the edit hook
// recorded is the edit the shadow records see, by its id, file and time.
func TestShadowWorld_ReadsTheEditLedgerTheEditHookWrote(t *testing.T) {
	root := ledgerRepo(t)
	file := filepath.Join(root, "src/widget.rs")
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	id := recordEdit(root, file)
	if id == "" {
		t.Fatal("the edit was not recorded")
	}

	got := shadowWorld().Edits(root)
	if len(got) != 1 || got[0].ID != id || got[0].File != file || got[0].At.IsZero() {
		t.Errorf("edits = %+v, want the recorded edit %s of %s with its time", got, id, file)
	}
	if lane := shadowWorld().Lane(root); lane == "" {
		t.Error("a checkout on a branch has no lane")
	}
	if st, err := shadowWorld().Open(root); err != nil || st == nil {
		t.Errorf("Open = %v, %v, want the repo's store", st, err)
	}
}

// An edit is folded into its lane's record by the flush after the hook, with no run:
// a test file the hook saw edited leaves its unit pending before any run finishes.
func TestPostEdit_FoldsATestEditIntoItsLanesRecordAtTheFlush(t *testing.T) {
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	mustWrite(t, filepath.Join(linked, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	test := filepath.Join(linked, "pkg", "p_test.go")
	mustWrite(t, test, "package pkg\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n")

	PostEdit(postPayload("Edit", test), fakeRun(true, "ok\nPASS"))
	if n := len(eventsOfKind(linked, "shadow")); n != 0 {
		t.Fatalf("%d shadow events before the flush, want none", n)
	}
	shadow.Flush()

	st, err := shadowWorld().Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := st.Load(context.Background(), LaneOf(linked))
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Units["pkg"].Phase; got != kernel.PhasePending {
		t.Errorf("unit pkg phase = %q, want pending: the test edit is folded with no run to judge it", got)
	}
}

// A run's own command decides which of the units its edits touched it stamps: a run
// of ./pkg/a says nothing of an edit in pkg/b.
func TestQueueShadowRun_FoldsTheRunOnlyIntoTheUnitsItsCommandNames(t *testing.T) {
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	mustWrite(t, filepath.Join(linked, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	a := filepath.Join(linked, "pkg", "a", "a.go")
	b := filepath.Join(linked, "pkg", "b", "b.go")
	mustWrite(t, a, "package a\n")
	mustWrite(t, b, "package b\n")
	ids := recordEdit(linked, a) + shadowEditIDSep + recordEdit(linked, b)

	queueShadowRun("run", PhaseOutcome{TreeKey: "abc1", ExitCode: 1, RunID: "r1"}, SuiteResult{Output: "--- FAIL: TestA\nFAIL\n"}, linked, "s-argv", ids, []string{"go", "test", "./pkg/a"}, "red")
	shadow.Flush()

	st, err := shadowWorld().Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := st.Load(context.Background(), LaneOf(linked))
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Units["pkg/a"].LastReal; got != kernel.VerdictRed {
		t.Errorf("unit pkg/a last real = %q, want the red of the run that named it", got)
	}
	if u, ok := rec.Units["pkg/b"]; ok && u.LastReal != "" {
		t.Errorf("unit pkg/b = %+v, want no verdict from a run that did not name it", u)
	}
}
