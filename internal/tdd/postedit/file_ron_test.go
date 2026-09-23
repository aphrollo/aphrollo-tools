package postedit

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifyFile_RonIsSource pins that a RON file is code as far as the
// gates are concerned. In a Rust workspace, `.ron` holds the embedded item
// registries, the locomotion key tables and the frozen schema fixtures —
// editing one changes program behaviour exactly as editing a `.rs` does, and
// classifying it Ignore meant those edits ran NO tests at all.
func TestClassifyFile_RonIsSource(t *testing.T) {
	// Classification is answered against a real tree, because a .ron is
	// Source only where a [package] owns it: see
	// TestClassifyFile_RonWithNoOwningCrateIsIgnored.
	root := t.TempDir()
	write(t, root, "crates/item/Cargo.toml", "[package]"+"\n"+`name = "item"`+"\n")
	for _, rel := range []string{
		"crates/item/assets/items.ron",
		"crates/item/data/loco_keys.ron",
		"crates/item/config/feel.ron",
		"crates/item/tests/fixtures/item_v1.ron",
	} {
		write(t, root, rel, "( )\n")
		if got := ClassifyFile(filepath.Join(root, rel)); got != Source {
			t.Errorf("ClassifyFile(%q) = %v, want source", rel, got)
		}
	}

	// The exclusions every other extension already obeys still apply.
	for _, rel := range []string{"node_modules/pkg/data.ron", "vendor/x/data.ron", "crates/item/testdata/data.ron"} {
		write(t, root, rel, "( )\n")
		if got := ClassifyFile(filepath.Join(root, rel)); got != Ignore {
			t.Errorf("ClassifyFile(%q) = %v, want ignore", rel, got)
		}
	}
}

// TestPostEdit_RonEditRunsTheOwningCratesTests pins the end of the story:
// editing a RON file inside a cargo crate runs THAT crate's tests, resolved
// through the nearest ancestor Cargo.toml — the same resolution a .rs edit
// gets. Before this the hook was silent, which reads as "nothing to test"
// for a file that can change the game's behaviour outright.
func TestPostEdit_RonEditRunsTheOwningCratesTests(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	write(t, root, "Cargo.toml", "[package]\nname = \"item\"\nversion = \"0.1.0\"\n")
	write(t, root, "src/lib.rs", "pub fn x() -> i32 { 1 }\n")
	write(t, root, "assets/items.ron", "([])\n")

	var seen []Runner
	got := PostEdit(postPayload("Edit", root+"/assets/items.ron"), func(r Runner, _ string) SuiteResult {
		seen = append(seen, r)
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	if len(seen) != 1 {
		t.Fatalf("a .ron edit must run the owning crate's tests, ran %d commands: %+v", len(seen), seen)
	}
	if args := strings.Join(seen[0].Args, " "); !strings.Contains(args, "-p item") {
		t.Fatalf("run = %q, want it scoped to the owning package (-p item)", args)
	}
	if !strings.Contains(got, "green") {
		t.Fatalf("the hook must report the outcome, got: %s", got)
	}
}
