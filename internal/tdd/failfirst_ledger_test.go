package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the commit gate's inline-Rust path (#714) the way a
// session does: each file state is written, recorded by the edit ledger as
// the edit hook would, given the verdict its run reached, and finally staged.

const redWidgetDoubles = "test widget::tests::widget_doubles ... FAILED\n"
const greenWidgetDoubles = "test widget::tests::widget_doubles ... ok\n"

// ledgerStep writes content to rel, records it as one edit, and attaches the
// run's verdict. It returns the edit id.
func ledgerStep(t *testing.T, root, rel, content string, outcome Outcome, output string) string {
	t.Helper()
	write(t, root, rel, content)
	id := recordEdit(root, filepath.Join(root, rel))
	if id == "" {
		t.Fatal("setup: the edit was not recorded")
	}
	recordEditVerdict(root, id, "cargo test --lib widget", outcome, output)
	return id
}

func noSuite(t *testing.T) SuiteRunner {
	return func(Runner, string) SuiteResult {
		t.Fatal("the inline-test path must not run a suite")
		return SuiteResult{}
	}
}

// inlineFailFirst stages everything and runs the stage for src/widget.rs,
// returning its stderr and the gate.log contents.
func inlineFailFirst(t *testing.T, root string, srcs ...string) (string, string) {
	t.Helper()
	gitDo(t, root, "add", ".")
	if len(srcs) == 0 {
		srcs = []string{"src/widget.rs"}
	}
	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStageWithRustNotice(root, root, nil, srcs, noSuite(t))
	})
	if res.Blocked {
		t.Fatalf("the inline-test path never blocks, got: %s", res.Message)
	}
	logData, _ := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "gate-state", "gate.log"))
	return stderr, string(logData)
}

func requireInconclusive(t *testing.T, stderr string) {
	t.Helper()
	if strings.Contains(stderr, "red-proven") || !strings.Contains(stderr, "inconclusive") {
		t.Fatalf("expected the unchanged inconclusive line, got: %s", stderr)
	}
}

func TestInlineFailFirst_LedgerRedThenGreenIsRedProven(t *testing.T) {
	root := ledgerRepo(t)
	red := ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, Red, redWidgetDoubles)
	green := ledgerStep(t, root, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)

	stderr, log := inlineFailFirst(t, root)
	if !strings.Contains(stderr, "[fail-first]") || !strings.Contains(stderr, "red-proven") {
		t.Fatalf("a ledger red→green on a test-only edit must prove RED, got: %s", stderr)
	}
	if !strings.Contains(stderr, red) || !strings.Contains(stderr, green) {
		t.Fatalf("the line must name both edits it relied on (%s, %s), got: %s", red, green, stderr)
	}
	if !strings.Contains(stderr, "widget_doubles") {
		t.Fatalf("the line must name the proven test, got: %s", stderr)
	}
	if !strings.Contains(log, "red-proven") {
		t.Fatalf("gate.log must record the proof, got:\n%s", log)
	}
}

// A compile failure is the usual inline RED: the test calls what does not
// exist yet, and nothing runs to be named.
func TestInlineFailFirst_MissingImplRedCounts(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, RedMissingImpl,
		"error[E0425]: cannot find function `widget2` in this scope\n")
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)

	stderr, _ := inlineFailFirst(t, root)
	if !strings.Contains(stderr, "red-proven") {
		t.Fatalf("a missing-impl red on a test-only edit must count, got: %s", stderr)
	}
}

// The test arrived in the same edit as the implementation: its red says
// nothing about the code without the implementation.
func TestInlineFailFirst_RedOnAProductionEditIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", strings.Replace(ledgerWidgetWithTest, "{ 1 }", "{ 3 }", 1), Red, redWidgetDoubles)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

func TestInlineFailFirst_RedWithoutALaterGreenIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, Red, redWidgetDoubles)
	write(t, root, "src/widget.rs", ledgerWidgetFixed)

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// A red that another test's failure explains names a different test.
func TestInlineFailFirst_RedNamingAnotherTestIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, Red, "test widget::tests::other ... FAILED\n")
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// A green that followed only test-code edits did not come from an
// implementation: something other than production code made it pass.
func TestInlineFailFirst_GreenFromATestOnlyEditIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, Red, redWidgetDoubles)
	ledgerStep(t, root, "src/other.rs", "#[cfg(test)]\nmod tests {\n    #[test]\n    fn other() {}\n}\n", Green, greenWidgetDoubles)

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// The staged test is not the one that went red: its assertion changed after.
func TestInlineFailFirst_TestChangedAfterTheRedIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, Red, redWidgetDoubles)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)
	write(t, root, "src/widget.rs", strings.Replace(ledgerWidgetFixed, "assert_eq!(widget(), 2);", "assert!(widget() > 0);", 1))

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// A test helper the test calls is part of what it asserts: changed after the
// red, the red no longer describes the staged test.
func TestInlineFailFirst_HelperChangedAfterTheRedIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	withHelper := strings.Replace(ledgerWidgetWithTest, "    use super::*;\n",
		"    use super::*;\n\n    fn want() -> i32 { 2 }\n", 1)
	withHelper = strings.Replace(withHelper, "widget(), 2", "widget(), want()", 1)
	ledgerStep(t, root, "src/widget.rs", withHelper, Red, redWidgetDoubles)
	fixed := strings.Replace(withHelper, "{ 1 }", "{ 2 }", 1)
	ledgerStep(t, root, "src/widget.rs", fixed, Green, greenWidgetDoubles)
	write(t, root, "src/widget.rs", strings.Replace(fixed, "fn want() -> i32 { 2 }", "fn want() -> i32 { widget() }", 1))

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// Records made before the last commit describe work that is already
// history; they cannot prove this commit's test.
func TestInlineFailFirst_RecordsFromAnEarlierHeadAreNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetWithTest, Red, redWidgetDoubles)
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)
	write(t, root, "README.md", "unrelated\n")
	gitDo(t, root, "add", "README.md")
	gitDo(t, root, "commit", "-qm", "unrelated")

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// Another checkout's ledger says nothing about this one.
func TestInlineFailFirst_AnotherCheckoutsLedgerIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	other := makeCargoRepo(t)
	write(t, other, "src/lib.rs", "pub mod widget;\n")
	write(t, other, "src/widget.rs", ledgerWidgetImpl)
	gitDo(t, other, "add", ".")
	gitDo(t, other, "commit", "-qm", "widget")
	ledgerStep(t, other, "src/widget.rs", ledgerWidgetWithTest, Red, redWidgetDoubles)
	ledgerStep(t, other, "src/widget.rs", ledgerWidgetFixed, Green, greenWidgetDoubles)
	write(t, root, "src/widget.rs", ledgerWidgetFixed)

	stderr, _ := inlineFailFirst(t, root)
	requireInconclusive(t, stderr)
}

// The sibling-file shape: the test lives in widget_tests.rs, mounted by a
// #[cfg(test)] #[path] declaration in widget.rs.
func TestInlineFailFirst_PathMountedSiblingTestIsRedProven(t *testing.T) {
	root := ledgerRepo(t)
	decl := "\n#[cfg(test)]\n#[path = \"widget_tests.rs\"]\nmod tests;\n"
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetImpl+decl, Green, "")
	red := ledgerStep(t, root, "src/widget_tests.rs",
		"use super::*;\n\n#[test]\nfn widget_doubles() {\n    assert_eq!(widget(), 2);\n}\n", Red, redWidgetDoubles)
	green := ledgerStep(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n"+decl, Green, greenWidgetDoubles)

	stderr, _ := inlineFailFirst(t, root, "src/widget.rs", "src/widget_tests.rs")
	if !strings.Contains(stderr, "red-proven") || !strings.Contains(stderr, red) || !strings.Contains(stderr, green) {
		t.Fatalf("a mounted sibling test proven red→green must prove RED naming %s and %s, got: %s", red, green, stderr)
	}
}

// The same rule when the green lands on a different file than the test: the
// sibling's helper changed after both runs, so neither run saw the staged
// helper.
func TestInlineFailFirst_SiblingHelperChangedAfterTheRunsIsNotAProof(t *testing.T) {
	root := ledgerRepo(t)
	decl := "\n#[cfg(test)]\n#[path = \"widget_tests.rs\"]\nmod tests;\n"
	sibling := "use super::*;\n\nfn want() -> i32 { 2 }\n\n#[test]\nfn widget_doubles() {\n    assert_eq!(widget(), want());\n}\n"
	ledgerStep(t, root, "src/widget.rs", ledgerWidgetImpl+decl, Green, "")
	ledgerStep(t, root, "src/widget_tests.rs", sibling, Red, redWidgetDoubles)
	ledgerStep(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n"+decl, Green, greenWidgetDoubles)
	write(t, root, "src/widget_tests.rs", strings.Replace(sibling, "fn want() -> i32 { 2 }", "fn want() -> i32 { widget() }", 1))

	stderr, _ := inlineFailFirst(t, root, "src/widget.rs", "src/widget_tests.rs")
	requireInconclusive(t, stderr)
}
