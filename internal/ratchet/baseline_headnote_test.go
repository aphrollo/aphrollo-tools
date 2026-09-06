package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDeletedBaselineRows_ARemovedKeyCountsAsOneDeletion(t *testing.T) {
	n, err := DeletedBaselineRows(
		"crates/a/lib.rs | 500\ncrates/b/lib.rs | 10\n",
		"crates/b/lib.rs | 10\n",
		Counted)
	if err != nil {
		t.Fatalf("DeletedBaselineRows: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1", n)
	}
}

// TestDeletedBaselineRows_ALoweredCountIsNotADeletion pins the scoping call
// from #497: trimming a ceiling DOWN is the sanctioned, common direction —
// the row's key survives, only its recorded count changed — and must never
// read as a deletion, or the note would fire on every ordinary tightening
// commit within a week.
func TestDeletedBaselineRows_ALoweredCountIsNotADeletion(t *testing.T) {
	n, err := DeletedBaselineRows(
		"crates/a/lib.rs | 500\n",
		"crates/a/lib.rs | 300\n",
		Counted)
	if err != nil {
		t.Fatalf("DeletedBaselineRows: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0 — a count-only drop is not a row deletion", n)
	}
}

func TestDeletedBaselineRows_IdenticalTextsHaveNoDeletion(t *testing.T) {
	text := "crates/a/lib.rs | 500\ncrates/b/lib.rs | 10\n"
	n, err := DeletedBaselineRows(text, text, Counted)
	if err != nil {
		t.Fatalf("DeletedBaselineRows: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0", n)
	}
}

// TestBaselineHeadRegressionNotes_FiresOnlyForTheLawThatLostARow proves the
// hint is scoped per baseline file: a law whose file merely tightened stays
// silent, and only the one whose row vanished entirely gets a note, naming
// its own path and count.
func TestBaselineHeadRegressionNotes_FiresOnlyForTheLawThatLostARow(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".ratchet", "baselines", "clean.txt"), "crates/a.rs | 5\n")
	write(t, filepath.Join(root, ".ratchet", "baselines", "corrupt.txt"), "")
	regs := []RegressedBaseline{
		{Law: "clean", Path: ".ratchet/baselines/clean.txt", Form: Counted},
		{Law: "corrupt", Path: ".ratchet/baselines/corrupt.txt", Form: Counted},
	}
	head := map[string]string{
		".ratchet/baselines/clean.txt":   "crates/a.rs | 9\n",
		".ratchet/baselines/corrupt.txt": "crates/a.rs | 9\n",
	}
	notes := BaselineHeadRegressionNotes(root, regs, func(rels []string) map[string]string { return head })
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one", notes)
	}
	if !strings.Contains(notes[0], ".ratchet/baselines/corrupt.txt has 1 row deleted relative to HEAD that this run did not delete") {
		t.Errorf("note = %q", notes[0])
	}
	if !strings.Contains(notes[0], "git diff .ratchet/baselines/corrupt.txt") {
		t.Errorf("note must point at the command that distinguishes the cause: %q", notes[0])
	}
}

// TestBaselineHeadRegressionNotes_NoHeadCopyIsNeverEligible proves a law
// adopted in this very commit (no HEAD copy of its baseline at all) never
// produces a note — there is nothing to have lost a row relative to.
func TestBaselineHeadRegressionNotes_NoHeadCopyIsNeverEligible(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".ratchet", "baselines", "new-law.txt"), "")
	regs := []RegressedBaseline{{Law: "new-law", Path: ".ratchet/baselines/new-law.txt", Form: Counted}}
	notes := BaselineHeadRegressionNotes(root, regs, func(rels []string) map[string]string { return map[string]string{} })
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
}

// TestCheck_NamesTheRegressedBaselineForACallerToAskAboutHistory proves
// Check's own half of #497: a law with a finding names its baseline file and
// form, without doing any git I/O itself, so a caller on the refusing path
// can ask BaselineHeadRegressionNotes about it.
func TestCheck_NamesTheRegressedBaselineForACallerToAskAboutHistory(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.RegressedBaselines) != 1 {
		t.Fatalf("RegressedBaselines = %+v, want exactly one", res.RegressedBaselines)
	}
	rb := res.RegressedBaselines[0]
	if rb.Law != "nan-guard" || rb.Path != ".ratchet/baselines/nan-guard.txt" || rb.Form != MultisetByText {
		t.Errorf("RegressedBaselines[0] = %+v", rb)
	}
}

// TestCheck_CleanRunNamesNoRegressedBaseline proves the field stays silent
// (and so no caller ever spends a git subprocess on it) when nothing
// regressed at all.
func TestCheck_CleanRunNamesNoRegressedBaseline(t *testing.T) {
	root := repoWithNanGuard(t)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.RegressedBaselines) != 0 {
		t.Errorf("RegressedBaselines = %+v, want none on a clean run", res.RegressedBaselines)
	}
}
