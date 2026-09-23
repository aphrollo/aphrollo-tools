package tdd

import (
	"strings"
	"testing"
)

// The field evidence behind this file: a `go test ./internal/tdd` run whose
// ONLY failing test was an environment failure (rustup with no default
// toolchain configured) still got classified red-missing-impl, and a
// separate run whose only failing test was a plain `t.Fatalf` assertion
// ("the suite ran in ..., want ...") got the same wrong verdict twice.
// Neither failing test's own message names a missing symbol. What both runs
// shared: a COMPLETELY UNRELATED, PASSING sibling test in the same package
// prints — as its own fixture data, proving the gate's OWN classifier
// recognises a fake build failure — a line containing "undefined: X". go
// test -json's per-test attribution says exactly which Test that line
// belongs to; ClassifyOutcome scanning the whole concatenated blob could
// not tell it apart from a real one.

// fixtureLeakJSON is the shape: TestFixtureLogsABuildFailureLine PASSES
// (asserting the ratchet fixture stage correctly reports "undefined:
// carriesTrigger" against a fake build) while TestPlainAssertionFails FAILS
// on a plain numeric comparison that names no symbol at all.
const fixtureLeakJSON = `{"Action":"run","Package":"pkg","Test":"TestFixtureLogsABuildFailureLine"}
{"Action":"output","Package":"pkg","Test":"TestFixtureLogsABuildFailureLine","Output":"gate precommit: ratchet fixture in /tmp/x -> REJECTED (go build: undefined: carriesTrigger)\n"}
{"Action":"output","Package":"pkg","Test":"TestFixtureLogsABuildFailureLine","Output":"--- PASS: TestFixtureLogsABuildFailureLine (0.01s)\n"}
{"Action":"pass","Package":"pkg","Test":"TestFixtureLogsABuildFailureLine","Elapsed":0.01}
{"Action":"run","Package":"pkg","Test":"TestPlainAssertionFails"}
{"Action":"output","Package":"pkg","Test":"TestPlainAssertionFails","Output":"--- FAIL: TestPlainAssertionFails (0.00s)\n"}
{"Action":"output","Package":"pkg","Test":"TestPlainAssertionFails","Output":"    x_test.go:10: the suite ran in 1.2s, want under 1s\n"}
{"Action":"fail","Package":"pkg","Test":"TestPlainAssertionFails","Elapsed":0}
{"Action":"output","Package":"pkg","Output":"FAIL\tpkg\t0.02s\n"}
{"Action":"fail","Package":"pkg","Elapsed":0.02}
`

// fixtureLeakFlatText is the naive whole-run concatenation the pre-fix
// classifier read (what renderGoTestJSON's human reconstruction gives): the
// passing test's fixture line and the real failure sit in the same blob.
const fixtureLeakFlatText = "gate precommit: ratchet fixture in /tmp/x -> REJECTED (go build: undefined: carriesTrigger)\n" +
	"--- PASS: TestFixtureLogsABuildFailureLine (0.01s)\n" +
	"--- FAIL: TestPlainAssertionFails (0.00s)\n" +
	"    x_test.go:10: the suite ran in 1.2s, want under 1s\n" +
	"FAIL\tpkg\t0.02s\n"

// TestClassificationOutput_IgnoresAPassingSiblingsFixtureText pins the
// scoping fix directly: given the structured go test -json stream,
// classificationOutput must drop the passing test's "undefined: ..." fixture
// line and keep the real failure's own text.
func TestClassificationOutput_IgnoresAPassingSiblingsFixtureText(t *testing.T) {
	got := classificationOutput(fixtureLeakFlatText, fixtureLeakJSON)
	if strings.Contains(got, "undefined: carriesTrigger") {
		t.Fatalf("classificationOutput leaked a PASSING sibling's fixture text: %q", got)
	}
	if !strings.Contains(got, "the suite ran in 1.2s, want under 1s") {
		t.Fatalf("classificationOutput dropped the real failure's own text: %q", got)
	}
}

// TestClassificationOutput_KeepsBuildFailureOutputWithNoTestName guards the
// other direction: a real compile/link failure carries no Test name at all
// (Action="output"/"build-output" with Test==""), and that text is the
// failure itself — it must never be scoped away just because it is not
// attributed to any one test.
func TestClassificationOutput_KeepsBuildFailureOutputWithNoTestName(t *testing.T) {
	buildFailJSON := `{"Action":"output","Package":"","Output":"./x_test.go:6:2: undefined: filepath\n"}
{"Action":"output","Package":"pkg","Output":"FAIL\tpkg [build failed]\n"}
{"Action":"fail","Package":"pkg","Elapsed":0}
`
	got := classificationOutput("undefined: filepath\nFAIL [build failed]", buildFailJSON)
	if !strings.Contains(got, "undefined: filepath") {
		t.Fatalf("classificationOutput dropped a build failure's own (untestable) output: %q", got)
	}
}

// TestClassificationOutput_FallsBackWithoutJSON guards the non-Go runners
// (cargo, pytest, vitest, zig — RunSuite never sets GoTestJSON for them):
// with no JSON to scope against, the original output stands unchanged.
func TestClassificationOutput_FallsBackWithoutJSON(t *testing.T) {
	got := classificationOutput("plain output text", "")
	if got != "plain output text" {
		t.Fatalf("classificationOutput(_, \"\") = %q, want the output unchanged", got)
	}
}

// TestPostEdit_UnrelatedPassingTestsFixtureText_StaysPlainRed is the
// end-to-end pin: PostEdit must classify the fixtureLeak run as a plain red
// (a real, unrelated assertion failure), never red-missing-impl, even though
// the run's flattened output (what a pre-fix classifier read) contains
// "undefined: carriesTrigger" from a passing sibling.
func TestPostEdit_UnrelatedPassingTestsFixtureText_StaysPlainRed(t *testing.T) {
	root := mkProject(t, "go.mod")
	res := SuiteResult{Passed: false, Output: fixtureLeakFlatText, GoTestJSON: fixtureLeakJSON}

	got := PostEdit(postPayload("Edit", root+"/widget.go"), fakeRunResult(res))

	if strings.Contains(got, string(RedMissingImpl)) {
		t.Fatalf("an unrelated passing test's fixture text must not read as red-missing-impl, got: %s", got)
	}
	if !strings.Contains(got, "outcome=red") {
		t.Fatalf("a real, unrelated assertion failure must stay red, got: %s", got)
	}
}

// TestClassifyOutcome_GoMissingImportIsBogusNotMissingImpl pins the second
// half of the fix: "undefined: filepath" and "undefined: NewWidget" are
// BYTE-IDENTICAL compiler shapes for two different causes (an unimported
// standard-library package vs. a production symbol nobody wrote yet) — the
// package's own name is the only signal telling them apart. A missing
// import is a broken TEST FILE (red-bogus), not a clean RED.
func TestClassifyOutcome_GoMissingImportIsBogusNotMissingImpl(t *testing.T) {
	got := ClassifyOutcome(false, "internal/tdd/mutants_go_test.go:252:23: undefined: filepath", nil)
	if got != RedBogus {
		t.Fatalf("ClassifyOutcome(undefined: filepath) = %q, want %q", got, RedBogus)
	}
}

// TestClassifyOutcome_GoMissingImportDoesNotSwallowARealMissingSymbol guards
// the direction the fix must not overreach: a genuine missing-impl RED
// naming a production identifier (not a recognised stdlib package name)
// keeps its class exactly as before.
func TestClassifyOutcome_GoMissingImportDoesNotSwallowARealMissingSymbol(t *testing.T) {
	got := ClassifyOutcome(false, "./x_test.go:9: undefined: NewWidget", nil)
	if got != RedMissingImpl {
		t.Fatalf("ClassifyOutcome(undefined: NewWidget) = %q, want %q (unchanged)", got, RedMissingImpl)
	}
}
