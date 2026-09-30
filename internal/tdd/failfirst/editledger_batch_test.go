package failfirst

import (
	"path/filepath"
	"strings"
	"testing"
)

// A Bash call that changes several files records one edit per file, and its
// one run settles them all.
func TestRecordEdits_RecordsOneEditPerFileAndJoinsTheIDs(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n")
	write(t, root, "src/other.rs", "pub fn other() -> i32 { 3 }\n")

	joined := recordEdits(root, []string{filepath.Join(root, "src/widget.rs"), filepath.Join(root, "src/other.rs")})

	ids := strings.Split(joined, ",")
	if len(ids) != 2 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("ids = %q, want two distinct ids", joined)
	}
	edits := loadEditLedger(root)
	if len(edits) != 2 || edits[0].ID != ids[0] || edits[1].ID != ids[1] {
		t.Fatalf("ledger = %+v, want the two edits in order under %v", edits, ids)
	}
	if !strings.HasSuffix(edits[0].File, "widget.rs") || !strings.HasSuffix(edits[1].File, "other.rs") {
		t.Fatalf("ledger files = %q, %q, want widget.rs then other.rs", edits[0].File, edits[1].File)
	}
}

func TestRecordEdits_NothingToRecordIsNoID(t *testing.T) {
	root := ledgerRepo(t)
	if got := recordEdits(root, nil); got != "" {
		t.Fatalf("recordEdits of no files = %q, want none", got)
	}
}

// One verdict, several edit ids: each edit carries the verdict of the run.
func TestRecordEditVerdict_SettlesEveryEditAJoinedIDNames(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n")
	write(t, root, "src/other.rs", "pub fn other() -> i32 { 3 }\n")
	joined := recordEdits(root, []string{filepath.Join(root, "src/widget.rs"), filepath.Join(root, "src/other.rs")})

	recordEditVerdict(root, joined, "cargo test --lib", Green, "test widget::tests::a ... ok\n")

	edits := loadEditLedger(root)
	if len(edits) != 2 {
		t.Fatalf("ledger = %+v, want two edits", edits)
	}
	for _, e := range edits {
		if e.Verdict == nil || e.Verdict.Outcome != string(Green) || e.Verdict.Cmd != "cargo test --lib" {
			t.Errorf("edit %s has verdict %+v, want the run's green", e.File, e.Verdict)
		}
	}
}

// An empty id names no edit: nothing is appended to the ledger.
func TestRecordEditVerdict_AnEmptyIDAppendsNothing(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n")
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))
	before := len(readLedgerLines(editLedgerPath(root)))

	recordEditVerdict(root, "", "cargo test --lib", Green, "")

	if after := len(readLedgerLines(editLedgerPath(root))); after != before {
		t.Fatalf("an empty id appended %d line(s)", after-before)
	}
	if e := loadEditLedger(root); len(e) != 1 || e[0].ID != id || e[0].Verdict != nil {
		t.Fatalf("ledger = %+v, want the one edit unsettled", e)
	}
}
