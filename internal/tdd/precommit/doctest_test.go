package precommit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctestRunnersCoverOnlyTheCratesThatHaveDoctests(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, "crates", "shared"), "Cargo.toml", "[package]\nname = \"shared\"\n")
	write(t, filepath.Join(ws, "crates", "shared", "src"), "lib.rs",
		"/// Adds.\n///\n/// ```compile_fail\n/// let x: u8 = shared::add(1);\n/// ```\npub fn add() {}\n")
	write(t, filepath.Join(ws, "crates", "movement"), "Cargo.toml", "[package]\nname = \"movement\"\n")
	write(t, filepath.Join(ws, "crates", "movement", "src"), "lib.rs",
		"// a plain comment with ``` in it\npub fn step() {}\n")

	runners := doctestRunners(ws, []string{"shared", "movement", "absent"})
	if len(runners) != 1 {
		t.Fatalf("runners = %+v — only a crate with doctests earns a run", runners)
	}
	if got := cargoArgsOf(runners[0]); got != "test -p shared --doc" {
		t.Errorf("args = %q — nextest never runs doctests, so this one is plain cargo test", got)
	}
	if runners[0].Dir != ws {
		t.Errorf("dir = %q, want the workspace root %q", runners[0].Dir, ws)
	}
}

func TestPackageHasDoctestsIgnoresOrdinaryCodeFences(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src"), "lib.rs", "fn f() {\n    let s = \"```\";\n}\n")
	if packageHasDoctests(dir) {
		t.Error("a fence in a string literal is not a doctest")
	}
	write(t, filepath.Join(dir, "src", "nested"), "mod.rs", "//! ```\n//! use x;\n//! ```\n")
	if !packageHasDoctests(dir) {
		t.Error("a module-level doc fence anywhere under src/ is a doctest")
	}
}

// nextest never runs doctests, so the gate has to run them itself — otherwise
// a `compile_fail` proof is a test that certifies what it never executed.
func TestPrecommitRunsDoctestsForACrateThatHasThem(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs",
		"/// Adds.\n///\n/// ```compile_fail\n/// let _: u8 = m::add();\n/// ```\npub fn add() {}\n")
	gitDo(t, root, "add", ".")

	var seen []string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		seen = append(seen, cargoArgsOf(r))
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	if !containsArgs(seen, "test -p m --doc") {
		t.Fatalf("no doctest run happened: %q", seen)
	}
}

func TestPrecommitSkipsDoctestsForACrateWithout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn add() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		seen = append(seen, cargoArgsOf(r))
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	for _, args := range seen {
		if strings.Contains(args, "--doc") {
			t.Fatalf("a crate with no doc fence must not buy a cargo run: %q", seen)
		}
	}
}
