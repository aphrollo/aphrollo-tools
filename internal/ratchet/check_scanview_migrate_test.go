package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDatedBaseline(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", "dated_comment_py.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func notesContaining(res Result, sub string) []string {
	var out []string
	for _, n := range res.Notes {
		if strings.Contains(n, sub) {
			out = append(out, n)
		}
	}
	return out
}

// TestCheck_AnUnchangedLegacyRepoMigratesOnTheFirstTighteningCheck: the tree is
// exactly at its baseline as the old lexers read it, so the first check that
// tightens rewrites the baseline under the current lexers, stamps it, says so
// in one line, and stays green; the next check has nothing left to do.
func TestCheck_AnUnchangedLegacyRepoMigratesOnTheFirstTighteningCheck(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows)
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none on the migrating run", res.Findings)
	}
	got := readDatedBaseline(t, root)
	if !strings.HasPrefix(got, ScanViewStamp+"\n") || strings.Count(got, "\n") != 4 {
		t.Fatalf("baseline = %q, want the stamp and the 3 rows the current lexers read", got)
	}
	want := "migrated dated_comment_py to the current lexers (3 rows)"
	if n := notesContaining(res, "migrated dated_comment_py"); len(n) != 1 || n[0] != want {
		t.Fatalf("migration notes = %q, want exactly %q", n, want)
	}
	if n := notesContaining(res, "dated_comment_py: its baseline"); len(n) != 0 {
		t.Errorf("legacy notes = %q, want none for a law that just migrated", n)
	}

	again, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if len(again.Findings) != 0 || len(notesContaining(again, "migrated")) != 0 {
		t.Fatalf("second run findings=%+v notes=%q, want green and silent", again.Findings, again.Notes)
	}
	if readDatedBaseline(t, root) != got {
		t.Error("second run rewrote the migrated baseline")
	}
}

// TestCheck_ALegacyTreeBelowItsBaselineMigratesToTheRecomputedRows: a row the
// tree no longer holds does not survive the migration.
func TestCheck_ALegacyTreeBelowItsBaselineMigratesToTheRecomputedRows(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows+"app/gone.py | # 2020-01-01 x\n")
	if _, err := Check(Options{Root: root, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := readDatedBaseline(t, root)
	if strings.Contains(got, "gone.py") || !strings.HasPrefix(got, ScanViewStamp+"\n") {
		t.Fatalf("baseline = %q, want the stamp and no row for the vanished file", got)
	}
}

// TestCheck_ALegacyTreeAboveItsBaselineIsNotMigrated: one hit over the ceiling
// the old lexers read is a real regression; the baseline stays as it was and
// the law stays legacy.
func TestCheck_ALegacyTreeAboveItsBaselineIsNotMigrated(t *testing.T) {
	root := scanViewRepo(t, "")
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the one real regression", res.Findings)
	}
	if got := readDatedBaseline(t, root); got != "" {
		t.Fatalf("baseline = %q, want it untouched", got)
	}
	if n := notesContaining(res, "migrated"); len(n) != 0 {
		t.Errorf("migration notes = %q, want none", n)
	}
	if n := notesContaining(res, "dated_comment_py: its baseline"); len(n) != 1 {
		t.Errorf("legacy notes = %q, want the law still named as legacy", n)
	}
}

// TestCheck_AnotherLawsRegressionLeavesALegacyBaselineUnmigrated: a run that
// reports any regression writes no baseline at all.
func TestCheck_AnotherLawsRegressionLeavesALegacyBaselineUnmigrated(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows)
	write(t, filepath.Join(root, ".ratchet", "baselines", "except_pass_api.txt"), "")
	write(t, filepath.Join(root, "app", "bad.py"), "try:\n    pass\nexcept Exception:\n    pass\n")
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("want the except_pass_api regression from app/bad.py")
	}
	if got := readDatedBaseline(t, root); got != oldBuildDatedRows {
		t.Fatalf("baseline = %q, want it untouched by a refusing run", got)
	}
}

// TestCheck_ANonTighteningRunNeverMigrates: report-only and proposed-content
// runs write nothing and keep the legacy note.
func TestCheck_ANonTighteningRunNeverMigrates(t *testing.T) {
	for name, opts := range map[string]Options{
		"report only": {},
		"proposed":    {Tighten: true, Proposed: map[string]string{"app/ai.py": phantomSpanPy}},
	} {
		root := scanViewRepo(t, oldBuildDatedRows)
		opts.Root = root
		res, err := Check(opts)
		if err != nil {
			t.Fatalf("%s: Check: %v", name, err)
		}
		if got := readDatedBaseline(t, root); got != oldBuildDatedRows {
			t.Errorf("%s: baseline = %q, want it untouched", name, got)
		}
		if n := notesContaining(res, "migrated"); len(n) != 0 {
			t.Errorf("%s: migration notes = %q, want none", name, n)
		}
		if n := notesContaining(res, ScanViewStamp); len(n) != 2 {
			t.Errorf("%s: legacy notes = %q, want one per legacy law", name, n)
		}
	}
}

// TestMigratedBaselineText_IsTheRecomputationTheGuardCompares: the text a
// staged migration must equal, from the legacy rows and the tree read under
// the current lexers; a stamped baseline has nothing to recompute.
func TestMigratedBaselineText_IsTheRecomputationTheGuardCompares(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows)
	rel := ".ratchet/baselines/dated_comment_py.txt"
	text, ok, err := MigratedBaselineText(Options{Root: root}, rel, oldBuildDatedRows)
	if err != nil || !ok {
		t.Fatalf("MigratedBaselineText = %v, %v", ok, err)
	}
	if !strings.HasPrefix(text, ScanViewStamp+"\n") || strings.Count(text, "\n") != 4 {
		t.Fatalf("text = %q, want the stamp and 3 rows", text)
	}
	if _, ok, _ := MigratedBaselineText(Options{Root: root}, rel, ScanViewStamp+"\n"+oldBuildDatedRows); ok {
		t.Error("a stamped baseline is not migrated again")
	}
	if _, ok, _ := MigratedBaselineText(Options{Root: root}, ".ratchet/baselines/nobody.txt", oldBuildDatedRows); ok {
		t.Error("a baseline no law declares has no recomputation")
	}
}
