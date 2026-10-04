package suite

import (
	"strings"
	"testing"
	"time"
)

// These are suite's own tests of cargo_uncompiled.go, reached today only
// through internal/tdd/postedit's edit-hook tests.

// TestRustModDeclNamed_MatchesEveryDeclarationShapeThatMountsAFile pins the
// pattern: bare, pub, pub(crate), attributed and #[path]-mounted declarations
// all count.
func TestRustModDeclNamed_MatchesEveryDeclarationShapeThatMountsAFile(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"mod wear;",
		"pub mod wear;",
		"pub(crate) mod wear;",
		"#[cfg(test)]\nmod wear;",
		"#[cfg(test)] #[allow(dead_code)]\npub mod wear ;",
		"#[path = \"elsewhere/wear.rs\"]\nmod wear;",
		"    mod wear;",
	} {
		if !rustModDeclNamed("wear").MatchString(src) {
			t.Errorf("declaration %q was not matched", src)
		}
	}
}

// TestRustModDeclNamed_RejectsInlineModulesAndOtherNames pins the negatives: an
// inline module mounts no file, and a longer or different name is not this one.
func TestRustModDeclNamed_RejectsInlineModulesAndOtherNames(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"mod wear { fn f() {} }",
		"mod wearing;",
		"mod not_wear;",
		"// mod wear;",
		"fn mod_wear() {}",
	} {
		if rustModDeclNamed("wear").MatchString(src) {
			t.Errorf("%q must not match a declaration of wear", src)
		}
	}
}

// TestCargoNestedTestFileReachable_TheDirectoryEntryPointsAreAlwaysReached pins
// that mod.rs and main.rs are what cargo compiles regardless of what they
// declare about themselves.
func TestCargoNestedTestFileReachable_TheDirectoryEntryPointsAreAlwaysReached(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, rel := range []string{"tests/suite/mod.rs", "tests/suite/main.rs"} {
		if !cargoNestedTestFileReachable(root, rel) {
			t.Errorf("%s is an entry point and always reachable", rel)
		}
	}
}

// TestCargoNestedTestFileReachable_AFileIsReachedOnlyThroughItsDirectorysModRs
// pins the mount rule: declared by the sibling mod.rs is reached, undeclared or
// with no mod.rs at all is not.
func TestCargoNestedTestFileReachable_AFileIsReachedOnlyThroughItsDirectorysModRs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "tests/suite/mod.rs", "mod declared;\n")
	if !cargoNestedTestFileReachable(root, "tests/suite/declared.rs") {
		t.Error("a file named by a mod declaration is reached")
	}
	if cargoNestedTestFileReachable(root, "tests/suite/orphan.rs") {
		t.Error("a file no declaration names is not reached")
	}
	if cargoNestedTestFileReachable(root, "tests/other/helper.rs") {
		t.Error("a directory with no mod.rs mounts nothing")
	}
}

// Serial: appends to gate.log under its own CLAUDE_CONFIG_DIR, a process-wide env var.
// TestNotCompiledTerminal_AnUnreachedNestedFileInAPassingCargoRunIsNotCompiled
// pins the verdict: logged, and the advisory names the file.
func TestNotCompiledTerminal_AnUnreachedNestedFileInAPassingCargoRunIsNotCompiled(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	write(t, root, "tests/suite/mod.rs", "mod other;\n")
	write(t, root, "tests/suite/orphan.rs", "#[test] fn t() {}\n")
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}

	got := notCompiledTerminal(r, root, root+"/tests/suite/orphan.rs", SuiteResult{Passed: true, Duration: time.Second})
	if !strings.Contains(got, "NOT-COMPILED") || !strings.Contains(got, "tests/suite/orphan.rs") {
		t.Fatalf("terminal = %q, want the not-compiled advisory naming the file", got)
	}
	requireLoggedVerdict(t, cfg, NotCompiled)
}

// TestNotCompiledTerminal_EveryOtherShapeIsLeftToTheOrdinaryVerdict pins the
// empty answers: a non-cargo runner, a failed run, a flat test file, a
// reached nested file, and a target that cannot be related to the root.
func TestNotCompiledTerminal_EveryOtherShapeIsLeftToTheOrdinaryVerdict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "tests/suite/mod.rs", "mod reached;\n")
	cargo := Runner{Cmd: "cargo", Args: []string{"test"}}
	pass := SuiteResult{Passed: true}
	cases := map[string]string{
		"go runner":     notCompiledTerminal(Runner{Cmd: "go"}, root, root+"/tests/suite/orphan.rs", pass),
		"failed run":    notCompiledTerminal(cargo, root, root+"/tests/suite/orphan.rs", SuiteResult{}),
		"flat file":     notCompiledTerminal(cargo, root, root+"/tests/it.rs", pass),
		"reached file":  notCompiledTerminal(cargo, root, root+"/tests/suite/reached.rs", pass),
		"unrelatable":   notCompiledTerminal(cargo, "relative/root", "/abs/tests/suite/orphan.rs", pass),
		"outside tests": notCompiledTerminal(cargo, root, root+"/src/lib.rs", pass),
	}
	for name, got := range cases {
		if got != "" {
			t.Errorf("%s: terminal = %q, want none", name, got)
		}
	}
}

// TestNotCompiledAdvisory_SaysNothingAboutTheFileWasBuiltOrRun pins the line.
func TestNotCompiledAdvisory_SaysNothingAboutTheFileWasBuiltOrRun(t *testing.T) {
	t.Parallel()
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}
	got := notCompiledAdvisory(r, "/w", "tests/suite/orphan.rs", 2500*time.Millisecond)
	for _, part := range []string{"cargo test -p a", "in /w", "NOT-COMPILED", "2.5s", "tests/suite/orphan.rs", "NOT tested"} {
		if !strings.Contains(got, part) {
			t.Fatalf("advisory %q lacks %q", got, part)
		}
	}
}

// TestFmtSeconds_IsOneDecimalPlace pins the format the siblings share.
func TestFmtSeconds_IsOneDecimalPlace(t *testing.T) {
	t.Parallel()
	if got := fmtSeconds(1234 * time.Millisecond); got != "1.2s" {
		t.Fatalf("fmtSeconds = %q, want 1.2s", got)
	}
}

// TestCargoNamedTarget_NamesTheFileStemOrTheDirectory pins the example/bench
// naming: the stem for a file directly under the directory, the directory name
// for a multi-file one, and nothing when the directory is absent.
func TestCargoNamedTarget_NamesTheFileStemOrTheDirectory(t *testing.T) {
	t.Parallel()
	cases := []struct{ rel, dir, want string }{
		{"crates/a/examples/demo.rs", "examples", "demo"},
		{"crates/a/examples/multi/main.rs", "examples", "multi"},
		{"crates/a/benches/speed.rs", "benches", "speed"},
		{"crates/a/src/lib.rs", "examples", ""},
		{"crates/a/examples", "examples", ""},
	}
	for _, c := range cases {
		if got := cargoNamedTarget(c.rel, c.dir); got != c.want {
			t.Errorf("cargoNamedTarget(%q, %q) = %q, want %q", c.rel, c.dir, got, c.want)
		}
	}
}
