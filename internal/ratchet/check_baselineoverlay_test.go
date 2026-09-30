package ratchet

import (
	"path/filepath"
	"testing"
)

const nanGuardBaselinePath = ".ratchet/baselines/nan-guard.txt"

// TestCheck_AProposedBaselineIsTheOneTheTreeIsJudgedBy: the commit gate judges
// the index and hands the staged content of every file that differs on disk;
// a baseline is one of those files. A working-tree baseline tightened and left
// unstaged must not decide a commit that carries the row it dropped.
func TestCheck_AProposedBaselineIsTheOneTheTreeIsJudgedBy(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, nanGuardBaselinePath), "# tightened on disk, unstaged\n")
	staged := "# one known site\ncrates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"

	onDisk, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(onDisk.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the site the tightened disk baseline no longer records", onDisk.Findings)
	}
	res, err := Check(Options{Root: root, Proposed: map[string]string{nanGuardBaselinePath: staged}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — the staged baseline records the site", res.Findings)
	}
}

// TestCheck_AProposedBaselineDecidesALineCountLawsKeys: a line-count law reads
// its baseline's keys before the scan, to pick the bar each file is judged by.
func TestCheck_AProposedBaselineDecidesALineCountLawsKeys(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "module_size", maxLinesLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "module_size.txt"), "")
	write(t, filepath.Join(root, "big.py"), "x = 1\nx = 1\nx = 1\nx = 1\nx = 1\nx = 1\n")
	proposed := map[string]string{".ratchet/baselines/module_size.txt": "big.py | 6\n"}
	res, err := Check(Options{Root: root, Proposed: proposed})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — the staged baseline carries big.py at 6", res.Findings)
	}
}

// TestCheck_AProposedBaselineWithoutTheStampIsLegacy: which lexers a baseline
// was written under is read off the same text the rows are, so a staged
// baseline that lacks the stamp is not judged by a disk copy that has it.
func TestCheck_AProposedBaselineWithoutTheStampIsLegacy(t *testing.T) {
	root := scanViewRepo(t, ScanViewStamp+"\n"+oldBuildDatedRows)
	proposed := map[string]string{".ratchet/baselines/dated_comment_py.txt": oldBuildDatedRows}
	res, err := Check(Options{Root: root, Proposed: proposed})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — the staged, unstamped baseline is judged by the lexers it was written under", res.Findings)
	}
}
