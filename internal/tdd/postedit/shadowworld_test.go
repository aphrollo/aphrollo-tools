package postedit

import (
	"path/filepath"
	"testing"
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
