package suite

import "testing"

// Real `cargo test --lib --no-run --offline` output, captured on rustc
// 1.97.1 (8bab26f4f 2026-07-14) / cargo 1.97.1 (c980f4866 2026-06-30) — the
// toolchain on the box this gate runs on. E0425's message grew a `in module
// `<path>“ / `in this scope` qualifier this attribution logic never itself
// reads (missingImplRe only ever matches the leading "cannot find function"
// phrase), but the fixture is kept as CAPTURED TEXT, not retyped, so a real
// future wording change is caught here rather than in a hand-written
// approximation of it.
//
// This is the ONE place these three shapes are proven without a real cargo
// on PATH: internal/tdd/postedit/classify_attribution_test.go proves the
// same three scenarios end to end, but its rustcOutput helper skips outright
// when cargo is absent (CI does not carry a Rust toolchain), so this fixture
// test is what keeps that path covered in CI.
const (
	// A test module's own call to a function nobody wrote — src/mode_probe/project.rs:9,
	// inside its `#[cfg(test)] mod tests { ... }`.
	rustcMissingFnInlineTest = "   Compiling m v0.1.0 (/tmp/mini)\n" +
		"error[E0425]: cannot find function `limit_after_ramp` in module `super`\n" +
		" --> src/mode_probe/project.rs:9:27\n" +
		"  |\n" +
		"9 |         assert_eq!(super::limit_after_ramp(), 6);\n" +
		"  |                           ^^^^^^^^^^^^^^^^ not found in `super`\n" +
		"\n" +
		"For more information about this error, try `rustc --explain E0425`.\n" +
		"error: could not compile `m` (lib test) due to 1 previous error\n"

	// The same call, from a sibling file `#[cfg(test)] #[path = "..."] mod
	// tests;` mounts — src/mode_probe/project_tests.rs:3, carrying no
	// `#[cfg(test)]` of its own.
	rustcMissingFnMountedTest = "   Compiling m v0.1.0 (/tmp/mini)\n" +
		"error[E0425]: cannot find function `limit_after_ramp` in module `super`\n" +
		" --> src/mode_probe/project_tests.rs:3:23\n" +
		"  |\n" +
		"3 |     assert_eq!(super::limit_after_ramp(), 6);\n" +
		"  |                       ^^^^^^^^^^^^^^^^ not found in `super`\n" +
		"\n" +
		"For more information about this error, try `rustc --explain E0425`.\n" +
		"error: could not compile `m` (lib test) due to 1 previous error\n"

	// A PRODUCTION function reading a const another source file deleted —
	// src/mode_probe/project.rs:2, outside any `#[cfg(test)]` item (issue
	// #759's own shape: a missing-symbol diagnostic that is not a test's).
	rustcMissingConstInProdCode = "   Compiling m v0.1.0 (/tmp/mini)\n" +
		"error[E0425]: cannot find value `LIMIT` in module `crate::mode_probe::probe`\n" +
		" --> src/mode_probe/project.rs:2:31\n" +
		"  |\n" +
		"2 |     crate::mode_probe::probe::LIMIT\n" +
		"  |                               ^^^^^ not found in `crate::mode_probe::probe`\n" +
		"\n" +
		"For more information about this error, try `rustc --explain E0425`.\n" +
		"error: could not compile `m` (lib test) due to 1 previous error\n"
)

func TestClassifyRunOutcome_Rustc1_97MissingFnInInlineTestModuleStaysMissingImpl(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/mode_probe/project.rs", "pub fn limit() -> u32 {\n"+
		"    3\n"+
		"}\n"+
		"#[cfg(test)]\n"+
		"mod tests {\n"+
		"    #[test]\n"+
		"    fn the_limit_ramps_to_six() {\n"+
		"\n"+
		"        assert_eq!(super::limit_after_ramp(), 6);\n"+
		"    }\n"+
		"}\n")

	got := classifyRunOutcome(Runner{Cmd: "cargo"}, dir, SuiteResult{Passed: false, Output: rustcMissingFnInlineTest}, nil)

	if got != RedMissingImpl {
		t.Fatalf("got %v, want %v", got, RedMissingImpl)
	}
}

func TestClassifyRunOutcome_Rustc1_97MissingFnInMountedTestFileStaysMissingImpl(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/mode_probe/project.rs", "pub fn limit() -> u32 {\n"+
		"    3\n"+
		"}\n"+
		"#[cfg(test)]\n"+
		"#[path = \"project_tests.rs\"]\n"+
		"mod tests;\n")
	write(t, dir, "src/mode_probe/project_tests.rs",
		"#[test]\nfn the_limit_ramps_to_six() {\n    assert_eq!(super::limit_after_ramp(), 6);\n}\n")

	got := classifyRunOutcome(Runner{Cmd: "cargo"}, dir, SuiteResult{Passed: false, Output: rustcMissingFnMountedTest}, nil)

	if got != RedMissingImpl {
		t.Fatalf("got %v, want %v", got, RedMissingImpl)
	}
}

func TestClassifyRunOutcome_Rustc1_97MissingConstInProductionCodeIsRedNotMissingImpl(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/mode_probe/project.rs", "pub fn limit() -> u32 {\n"+
		"    crate::mode_probe::probe::LIMIT\n"+
		"}\n"+
		"#[cfg(test)]\n"+
		"mod tests {\n"+
		"    #[test]\n"+
		"    fn limit_is_three() {\n"+
		"        assert_eq!(super::limit(), 3);\n"+
		"    }\n"+
		"}\n")

	got := classifyRunOutcome(Runner{Cmd: "cargo"}, dir, SuiteResult{Passed: false, Output: rustcMissingConstInProdCode}, nil)

	if got != Red {
		t.Fatalf("got %v, want %v", got, Red)
	}
}
