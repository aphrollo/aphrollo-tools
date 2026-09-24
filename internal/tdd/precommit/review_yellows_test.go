package precommit

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifyFile_RonWithNoOwningCrateIsIgnored pins a full-workspace run:
// a .ron under a VIRTUAL workspace root (borld's assets/**) has no owning
// [package], so the scoped run resolved to an EMPTY package name and the
// gate fell back to the whole workspace — the heaviest possible run, from
// editing an asset.
func TestClassifyFile_RonWithNoOwningCrateIsIgnored(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]"+"\n"+`members = ["crates/*"]`+"\n")
	write(t, root, "assets/items/sword.ron", "( )\n")
	write(t, root, "crates/item/Cargo.toml", "[package]"+"\n"+`name = "item"`+"\n")
	write(t, root, "crates/item/registry.ron", "( )\n")

	if got := ClassifyFile(filepath.Join(root, "assets", "items", "sword.ron")); got != Ignore {
		t.Fatalf("assets/**.ron = %v, want Ignore — no owning crate means no scoped run", got)
	}
	if got := ClassifyFile(filepath.Join(root, "crates", "item", "registry.ron")); got != Source {
		t.Fatalf("a crate's own .ron = %v, want Source", got)
	}
}

// TestQualityStage_RejectsWhenNoSlotComesFree pins the asymmetry the review
// found: the mechanical suite REJECTS a commit it could not run, while
// clippy waved the same commit through. A check that did not run has proven
// nothing, and the two stages must agree about what that means.
func TestQualityStage_RejectsWhenNoSlotComesFree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\n"+`clippy-clean = ["a"]`+"\n")

	_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(ws), "cargo build", "/repo")
	if !ok {
		t.Fatal("could not occupy the slot")
	}
	defer release()
	defer SetPrecommitLockWait(0)()
	if pkgs := cargoClippyCleanPackages(ws); len(pkgs) != 1 || pkgs[0] != "a" {
		t.Fatalf("fixture problem: clippy-clean = %v", pkgs)
	}

	got := cargoQualityStage("precommit", ws, ws, []string{"a"}, func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true}
	}, ws, qualityClippy)
	if !got.Blocked {
		t.Fatal("clippy could not run, so nothing was proven — the commit must be rejected, not waved through")
	}
	if !strings.Contains(got.Message, "build slot") {
		t.Fatalf("message = %q, want it to name the busy build slot as the reason", got.Message)
	}
}
