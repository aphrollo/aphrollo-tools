package postedit

import (
	"path/filepath"
	"strings"
	"testing"
)

// Verbatim from the field (issue #715):
//
//	gate: cargo nextest run -p forge_solver --lib -E test(/^ground_contact::patch::build::/) → NO-TESTS-SELECTED (0 tests selected in 0.8s) — inconclusive, the code was NOT tested
//
// printed TWO LINES after the premerge gate ran forge_solver's whole suite
// GREEN. The module-size law puts this crate's tests in a sibling module the
// module filter can never match, so every narrowed edit there widens to the
// same package-scope command the premerge stage just ran and cached — the
// narrowed filter must stand down rather than repeat a question the
// mechanical green cache already answered, and it must never claim the code
// was NOT tested when the crate's suite ran moments before.
func TestPostEdit_NarrowedFilterStandsDown_WhenThisCratesFullSuiteAlreadyProvedGreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_solver\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "src/lib.rs", "pub mod ground_contact;\n")
	write(t, root, "src/ground_contact.rs", "pub mod patch;\n")
	write(t, root, "src/ground_contact/patch.rs", "pub mod build;\n")
	target := filepath.Join(root, "src", "ground_contact", "patch", "build.rs")
	write(t, root, "src/ground_contact/patch/build.rs",
		"pub fn compute() -> i32 { 1 }\n"+
			"#[cfg(test)]\nmod build_tests {\n    #[test]\n    fn compute_is_one() { assert_eq!(super::compute(), 1); }\n}\n")
	withNextest(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	// Reproduce exactly the command the narrowed filter resolves to, and its
	// widened (package-scope) form — the same one the premerge/precommit
	// mechanical stage runs and caches on a green result.
	runner, ok := DetectRunner(root)
	if !ok {
		t.Fatal("setup: DetectRunner must recognise the cargo crate")
	}
	narrow := NarrowToRelatedTests(runner, target, root)
	if got := cmdString(narrow); got != "cargo nextest run -p forge_solver --lib -E test(/^ground_contact::patch::build::/)" {
		t.Fatalf("setup: narrowed command = %q, want the exact field command", got)
	}
	full, widened := widenCargoRunner(narrow)
	if !widened {
		t.Fatal("setup: expected the narrowed runner to widen to package scope")
	}

	// The premerge stage already ran and cached exactly this: runSuiteStage's
	// own mechCacheAdd after a passing mechanical run.
	hash := worktreeStateHash(root)
	if hash == "" {
		t.Fatal("setup: worktreeStateHash must resolve for a committed repo")
	}
	mechCacheAdd(mechKey(root, hash, full))

	var seen []string
	refuse := func(r Runner, _ string) SuiteResult {
		seen = append(seen, cmdString(r))
		t.Fatalf("no suite may run when this crate's full suite already proved green in this state, got %q", cmdString(r))
		return SuiteResult{}
	}
	got := PostEdit(postPayload("Edit", target), refuse)

	if strings.Contains(got, strings.ToUpper(NoTestsSelected)) || strings.Contains(got, "NOT tested") {
		t.Fatalf("must never claim the code was not tested right after its suite ran green, got %q", got)
	}
	if !strings.Contains(got, "cache-hit") {
		t.Fatalf("expected a cache-hit advisory naming the crate's already-proven state, got %q", got)
	}
	if len(seen) != 0 {
		t.Fatalf("must not run any suite, ran %v", seen)
	}
}
