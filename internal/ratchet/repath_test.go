package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bigFileLaw = `
name = "big-file"
description = "modules stay small"
severity = "deny"
baseline = ".ratchet/baselines/big-file.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "line-count"
max = 5
`

// repathGitRepo builds a real git repo: one line-count law, oldRel committed
// at oldContent with a baseline row recording its line count, then oldRel
// removed on disk and newRel written with newContent and `git add -A`'d —
// the shape a `git mv` (plus, in the content-changed case, a hand edit)
// takes. The baseline file itself is left untouched, exactly as a plain
// rename leaves it: this is the file #490 is about, not the staged-baseline
// guard's OWN staged-baseline file.
func repathGitRepo(t *testing.T, oldRel, newRel, oldContent, newContent string, oldLines int) string {
	t.Helper()
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "big-file", bigFileLaw)
	write(t, filepath.Join(root, filepath.FromSlash(oldRel)), oldContent)
	write(t, filepath.Join(root, ".ratchet", "baselines", "big-file.txt"),
		oldRel+" | "+itoa(oldLines)+"\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	if err := os.Remove(filepath.Join(root, filepath.FromSlash(oldRel))); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, filepath.FromSlash(newRel)), newContent)
	gitRun(t, root, "add", "-A")
	return root
}

// TestCheckRepathsCountedRow_OnAPureRenameAtUnchangedContent is #490's RED: a
// `git mv` of a file carrying a Counted (file-identity) baseline row, with
// the row itself untouched, must report zero regressions and re-path the
// row onto the file's new name — not read the old row as gone and the new
// path as a brand-new file crossing the ceiling.
func TestCheckRepathsCountedRow_OnAPureRenameAtUnchangedContent(t *testing.T) {
	content := strings.Repeat("let x = 1;\n", 6)
	root := repathGitRepo(t, "crates/a/old.rs", "crates/a/new.rs", content, content, 6)

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a pure rename at unchanged count and content must not regress: %+v", res.Findings)
	}
	if len(res.Tightened) != 1 {
		t.Fatalf("the row moving to its new path is a write: %v", res.Tightened)
	}
	got := read(t, filepath.Join(root, ".ratchet", "baselines", "big-file.txt"))
	if got != "crates/a/new.rs | 6\n" {
		t.Errorf("baseline = %q, want the row re-pathed to crates/a/new.rs", got)
	}
}

// TestCheckRegressesRename_WhenContentAlsoChanged proves the pairing is not
// a blanket exemption: content is compared by git blob, not merely an
// unchanged line count, so a rename that also edited the file still reads
// as a new file over the ceiling.
func TestCheckRegressesRename_WhenContentAlsoChanged(t *testing.T) {
	root := repathGitRepo(t, "crates/a/old.rs", "crates/a/new.rs",
		strings.Repeat("let x = 1;\n", 6), strings.Repeat("let y = 2;\n", 6), 6)

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "crates/a/new.rs" {
		t.Fatalf("content changed at an unchanged count must still regress: %+v", res.Findings)
	}
	if len(res.Tightened) != 0 {
		t.Errorf("a regressing run must not tighten anything, got %v", res.Tightened)
	}
}

// TestCheckRegressesNewFile_WhenNoBaselineRowVanished is #490's other
// guardrail: a genuinely new file crossing the ceiling, with the existing
// baseline row's file still on disk untouched (nothing vanished), must
// still regress -- even at the SAME line count and the SAME content as the
// untouched file, which is the strongest case a buggy "vanished" check
// could get wrong (pairing on count-and-content alone, without first
// confirming the old row's file is actually gone).
func TestCheckRegressesNewFile_WhenNoBaselineRowVanished(t *testing.T) {
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "big-file", bigFileLaw)
	content := strings.Repeat("let x = 1;\n", 6)
	write(t, filepath.Join(root, "crates", "a", "old.rs"), content)
	write(t, filepath.Join(root, ".ratchet", "baselines", "big-file.txt"), "crates/a/old.rs | 6\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	// old.rs is untouched -- nothing vanished -- and a genuinely new file
	// crosses the ceiling at the SAME line count and the SAME content.
	write(t, filepath.Join(root, "crates", "b", "new.rs"), content)
	gitRun(t, root, "add", "-A")

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "crates/b/new.rs" {
		t.Fatalf("a genuinely new file over the ceiling must still regress: %+v", res.Findings)
	}
}

// TestCheckRefusingRun_LeavesEveryBaselineByteIdentical is #490's second
// defect: a run that reports a regression must not write ANY baseline,
// including one belonging to a completely unrelated, individually clean
// law — proven here by nan-guard's one known site getting fixed (which,
// alone, tightens: see TestCheckTightensTheBaselineWhenASiteIsFixed) while
// an unrelated big-file law crosses its ceiling in the same run.
func TestCheckRefusingRun_LeavesEveryBaselineByteIdentical(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")
	writeLaw(t, root, "big-file", bigFileLaw)
	write(t, filepath.Join(root, "crates", "b", "big.rs"), strings.Repeat("let x = 1;\n", 6))

	nanBefore := read(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("setup: expected big.rs to regress big-file's ceiling")
	}
	if len(res.Tightened) != 0 {
		t.Fatalf("a refusing run must not tighten anything, got %v", res.Tightened)
	}
	if got := read(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")); got != nanBefore {
		t.Errorf("nan-guard baseline changed on a refusing run: before %q, after %q", nanBefore, got)
	}
}
