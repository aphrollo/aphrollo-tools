package lawgate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repathRepo commits one counted baseline naming oldRel at oldCount with
// oldContent, then removes oldRel and stages a row at newRel with newCount
// and newContent — the shape a `git mv` plus a hand-fixed baseline row takes.
func repathRepo(t *testing.T, baselinePath, oldRel, newRel string, oldCount, newCount int, oldContent, newContent string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, oldRel), oldContent)
	mustWrite(t, filepath.Join(root, baselinePath), fmt.Sprintf("%s | %d\n", oldRel, oldCount))
	gitAddAll(t, root)
	commitAll(t, root)

	if err := os.Remove(filepath.Join(root, oldRel)); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, newRel), newContent)
	mustWrite(t, filepath.Join(root, baselinePath), fmt.Sprintf("%s | %d\n", newRel, newCount))
	gitAddAll(t, root)
	return root
}

// TestBaselineGuard_AllowsAPureRenameAtUnchangedCountAndContent is the RED
// this issue is about: a `git mv` of a file that carries a count-keyed
// baseline row, with the count and the file's bytes both unchanged, must not
// be refused as a brand-new key over the ceiling.
func TestBaselineGuard_AllowsAPureRenameAtUnchangedCountAndContent(t *testing.T) {
	content := strings.Repeat("let x = 1;\n", 647)
	root := repathRepo(t, ".ratchet/baselines/module_size.txt",
		"crates/forge_solver/tests/integration/kernel/bend.rs",
		"crates/forge_solver/tests/integration/bend.rs",
		647, 647, content, content)

	res := baselineStage("precommit", root)
	if res.Blocked {
		t.Fatalf("a pure rename at unchanged count and content must not reject: %s", res.Message)
	}
}

// TestBaselineGuard_RejectsARenameWhoseCountRose proves the one-way property
// survives the fix: a rename is never a way to launder a raise, even when the
// file's content did not change.
func TestBaselineGuard_RejectsARenameWhoseCountRose(t *testing.T) {
	content := strings.Repeat("let x = 1;\n", 700)
	root := repathRepo(t, ".ratchet/baselines/module_size.txt",
		"crates/a/old.rs", "crates/a/new.rs", 647, 700, content, content)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a rename whose count rose must still reject")
	}
	if !strings.Contains(res.Message, "crates/a/new.rs") || !strings.Contains(res.Message, "0 -> 700") {
		t.Errorf("message must read as a new key over the ceiling: %s", res.Message)
	}
}

// TestBaselineGuard_RejectsARenameWhoseContentChangedAtTheSameCount proves
// the count alone is never enough: content is verified by git blob, not
// merely an unchanged line count.
func TestBaselineGuard_RejectsARenameWhoseContentChangedAtTheSameCount(t *testing.T) {
	root := repathRepo(t, ".ratchet/baselines/module_size.txt",
		"crates/a/old.rs", "crates/a/new.rs", 647, 647,
		strings.Repeat("let x = 1;\n", 647), strings.Repeat("let y = 2;\n", 647))

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("content changed at an unchanged count must still reject")
	}
	if !strings.Contains(res.Message, "crates/a/new.rs") || !strings.Contains(res.Message, "0 -> 647") {
		t.Errorf("message must read as a new key over the ceiling: %s", res.Message)
	}
}

// TestBaselineGuard_DoesNotPairSameCountDifferentContentFilesByCountAlone
// proves pairing needs a content match per candidate, not just an available
// row of the same count: two rows disappear and two appear, all at 647, but
// neither new path's bytes match either old row, so nothing may pair merely
// because a same-count candidate exists.
func TestBaselineGuard_DoesNotPairSameCountDifferentContentFilesByCountAlone(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "crates/a/a.rs"), strings.Repeat("aaaa\n", 647))
	mustWrite(t, filepath.Join(root, "crates/a/b.rs"), strings.Repeat("bbbb\n", 647))
	mustWrite(t, filepath.Join(root, ".ratchet/baselines/module_size.txt"),
		"crates/a/a.rs | 647\ncrates/a/b.rs | 647\n")
	gitAddAll(t, root)
	commitAll(t, root)

	if err := os.Remove(filepath.Join(root, "crates/a/a.rs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "crates/a/b.rs")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "crates/a/na.rs"), strings.Repeat("cccc\n", 647))
	mustWrite(t, filepath.Join(root, "crates/a/nb.rs"), strings.Repeat("dddd\n", 647))
	mustWrite(t, filepath.Join(root, ".ratchet/baselines/module_size.txt"),
		"crates/a/na.rs | 647\ncrates/a/nb.rs | 647\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("two same-count different-content files must not pair merely by count")
	}
	for _, want := range []string{"crates/a/na.rs 0 -> 647", "crates/a/nb.rs 0 -> 647"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message must name %s: %s", want, res.Message)
		}
	}
}
