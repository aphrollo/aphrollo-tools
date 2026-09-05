package tdd

import (
	"reflect"
	"testing"
)

// Zig fixtures are real `zig build test` / `zig test` output captured from the
// zeta project (Zig 0.16.0). `zig build test` is what the tdd runner invokes;
// it surfaces assertion failures as `error: '<mod>.test.<name>' failed:` and
// compile errors with the `error:` diagnostic + (for @compileError) a
// `referenced by:` trail. The zero-tests summary (`All 0 tests passed.`) only
// shows on a direct `zig test <file>`, since `zig build test` stays silent on a
// clean/zero run — captured here so a direct run is still classified honestly.
const (
	// Clean direct run reporting a non-zero count — must stay Green, NOT
	// WritingTest (guards zeroTestsRe against over-matching "N tests passed").
	zigGreen = "All 150 tests passed.\n"

	// A file/step that ran zero tests.
	zigZeroTests = "All 0 tests passed.\n"

	// Plain assertion failure under `zig build test`.
	zigAssertFail = `test
+- run test 10 pass, 1 fail (11 total)
error: 'integration_test.test.deliberately failing assertion' failed:
       expected 1, found 2
       /home/u/.cache/zig/lib/std/testing.zig:118:17: 0x125f939 in expectEqualInner (std.zig)
                       return error.TestExpectedEqual;
                       ^
       /tmp/zeta/tests/integration_test.zig:604:5: 0x125f9eb in test.deliberately failing assertion (integration_test.zig)
           try std.testing.expectEqual(@as(usize, 1), @as(usize, 2));
           ^
Build Summary: 5/7 steps succeeded (1 failed); 150/151 tests passed (1 failed)
`

	// Undefined symbol under test — clean RED (missing impl).
	zigNoMember = `tests/integration_test.zig:604:19: error: root source file struct 'root' has no member named 'this_symbol_does_not_exist'
    const x = zeta.this_symbol_does_not_exist();
              ~~~~^~~~~~~~~~~~~~~~~~~~~~~~~~~
src/root.zig:1:1: note: struct declared here
error: 1 compilation errors
`
	zigUndeclared = `tests/integration_test.zig:604:28: error: use of undeclared identifier 'some_undeclared_thing'
    try std.testing.expect(some_undeclared_thing == 1);
                           ^~~~~~~~~~~~~~~~~~~~~
error: 1 compilation errors
`

	// Syntax / structural compile failures — RedBogus (fix the test, not impl).
	zigSyntax = `tests/integration_test.zig:604:15: error: expected expression, found '='
    const x = = 5;
              ^
error: 1 compilation errors
`
	zigReferencedBy = `tests/integration_test.zig:603:32: error: boom in genfn
fn genFn(comptime T: type) T { @compileError("boom in genfn"); }
                               ^~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
referenced by:
    test.compileError via referenced by: tests/integration_test.zig:605:14
error: 1 compilation errors
`
)

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name        string
		passed      bool
		output      string
		prevFailing []string
		want        Outcome
	}{
		{"clean pass", true, "ok  pkg  0.1s\nPASS", nil, Green},
		{"pass with warnings", true, "PASS\nwarning: unused import", nil, GreenWithWarnings},
		// A passing test edit is GREEN, not a tautology guess — post-edit can't
		// know if the impl pre-existed (backfill/split/refactor all pass).
		{"passing test edit is green", true, "ok\nPASS", nil, Green},
		// The 0-tests false-GREEN fix, across runners.
		{"go no tests", true, "testing: warning: no tests to run\nPASS", nil, WritingTest},
		{"pytest 0 collected", true, "collected 0 items", nil, WritingTest},
		{"vitest 0 tests", true, "Test Files  no tests\n0 tests", nil, WritingTest},
		// Zig: zero tests ran → WritingTest; a real count stays Green.
		{"zig 0 tests", true, zigZeroTests, nil, WritingTest},
		{"zig all passed is green", true, zigGreen, nil, Green},
		// Failures.
		{"missing impl is clean red", false, "./x_test.go:9: undefined: NewWidget", nil, RedMissingImpl},
		{"python import is bogus", false, "ERROR collecting tests/x.py\nModuleNotFoundError: no module named 'q'", nil, RedBogus},
		{"syntax error is bogus", false, "SyntaxError: invalid syntax", nil, RedBogus},
		{"plain assertion failure", false, "--- FAIL: TestThing\n  want 1 got 2", nil, Red},
		// Rust: the compiler's missing-symbol diagnostics are the clean-RED
		// signal (E0425 cannot find function/value, E0599 no method named,
		// E0433 use of undeclared crate or module).
		{"rust cannot-find-function is missing impl", false,
			"error[E0425]: cannot find function `widget` in this scope\n --> src/lib.rs:4:5", nil, RedMissingImpl},
		{"rust cannot-find-value is missing impl", false,
			"error[E0425]: cannot find value `WIDGET_MAX` in this scope", nil, RedMissingImpl},
		{"rust no-method is missing impl", false,
			"error[E0599]: no method named `frob` found for struct `Widget` in the current scope", nil, RedMissingImpl},
		{"rust undeclared crate is missing impl", false,
			"error[E0433]: failed to resolve: use of undeclared crate or module `widgets`", nil, RedMissingImpl},
		// A runtime "no such file or directory" in an assertion message is a
		// plain failure, not a missing implementation.
		{"runtime no-such-file is plain red", false,
			"--- FAIL: TestThing\n open /tmp/cfg.yaml: no such file or directory", nil, Red},
		// Zig failures: undefined symbol = clean RED; compile/syntax = bogus;
		// plain assertion = Red.
		{"zig no member is missing impl", false, zigNoMember, nil, RedMissingImpl},
		{"zig undeclared ident is missing impl", false, zigUndeclared, nil, RedMissingImpl},
		{"zig syntax error is bogus", false, zigSyntax, nil, RedBogus},
		{"zig referenced-by compile error is bogus", false, zigReferencedBy, nil, RedBogus},
		{"zig assertion failure is red", false, zigAssertFail, nil, Red},
		// Pre-existing failure → no-delta (don't nag).
		{"no new failures", false, "--- FAIL: TestOld\n want x", []string{"TestOld"}, NoDelta},
		{"a new failure breaks no-delta", false, "--- FAIL: TestNew\n want x", []string{"TestOld"}, Red},
		// The delta decides BEFORE the regex classes: a failure set already
		// recorded as failing is NoDelta even when its message text happens to
		// match a missing-impl or setup-error pattern. Otherwise pre-existing
		// breakage is relabeled red-missing-impl/red-bogus on every unrelated
		// edit and the agent is nagged about failures it did not cause.
		{"no-delta beats missing-impl text", false,
			"FAILED tests/x.py::test_old\nAttributeError: 'Widget' object has no attribute 'frob'",
			[]string{"tests/x.py::test_old"}, NoDelta},
		{"no-delta beats setup-error text", false,
			"FAILED tests/x.py::test_old\nImportError: cannot import name 'frob'",
			[]string{"tests/x.py::test_old"}, NoDelta},
		// A NEW failure still gets its regex class — delta precedence must not
		// blunt the clean-RED signal for a fresh missing symbol.
		{"fresh missing-impl still clean red", false,
			"FAILED tests/x.py::test_new\nAttributeError: 'Widget' object has no attribute 'frob'",
			[]string{"tests/x.py::test_old"}, RedMissingImpl},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyOutcome(c.passed, c.output, c.prevFailing); got != c.want {
				t.Fatalf("ClassifyOutcome = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExtractFailingTests(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []string
	}{
		{"go", "--- FAIL: TestA (0.00s)\n--- FAIL: TestB (0.00s)", []string{"TestA", "TestB"}},
		{"pytest", "FAILED tests/x.py::test_foo\ntests/x.py::test_bar FAILED", []string{"tests/x.py::test_bar", "tests/x.py::test_foo"}},
		{"cargo", "test mod::it ... FAILED", []string{"mod::it"}},
		// vitest name with parentheses must survive the duration strip.
		{"vitest parens", "  ✗ should handle (error cases) (12 ms)", []string{"should handle (error cases)"}},
		{"zig build test", zigAssertFail, []string{"integration_test.test.deliberately failing assertion"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractFailingTests(c.output); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ExtractFailingTests = %#v, want %#v", got, c.want)
			}
		})
	}
}

// TestGoRunIsVacuous_TrueWhenThePackageRanButExecutedNoTest pins the #194
// shape: a TestMain that returns (or calls os.Exit(0)) before m.Run() makes
// `go test -v` print an ordinary "ok" package summary with no per-test
// "--- PASS/FAIL/SKIP:" line behind it at all — the same text a package with
// a hundred passing tests would print if -v were stripped away, and the
// fact this function exists to keep that from happening at commit time.
func TestGoRunIsVacuous_TrueWhenThePackageRanButExecutedNoTest(t *testing.T) {
	output := "ok  \texample.com/m\t0.004s\n"
	if !goRunIsVacuous(output) {
		t.Fatalf("goRunIsVacuous(%q) = false, want true (a package that ran but tested nothing)", output)
	}
}

// TestGoRunIsVacuous_FalseWhenNoTestFilesExist keeps the legitimate empty
// pass (a package with no _test.go files at all, `?   pkg  [no test
// files]`) from being caught by the same rule: zeroTestsRe already treats
// this as an ordinary green, and #317 must not turn every commit touching a
// test-less package into a refusal.
func TestGoRunIsVacuous_FalseWhenNoTestFilesExist(t *testing.T) {
	output := "?   \texample.com/m\t[no test files]\n"
	if goRunIsVacuous(output) {
		t.Fatalf("goRunIsVacuous(%q) = true, want false (no test files is a legitimate empty pass)", output)
	}
}

// TestGoRunIsVacuous_FalseWhenTestsActuallyRan is the base case: a package
// whose tests really executed must never be flagged, whatever their count.
func TestGoRunIsVacuous_FalseWhenTestsActuallyRan(t *testing.T) {
	output := "=== RUN   TestWidget\n--- PASS: TestWidget (0.00s)\nPASS\nok  \texample.com/m\t0.004s\n"
	if goRunIsVacuous(output) {
		t.Fatalf("goRunIsVacuous(%q) = true, want false (a real test ran)", output)
	}
}

func TestOutcome_IsRed(t *testing.T) {
	red := []Outcome{RedMissingImpl, RedBogus, Red}
	for _, o := range red {
		if !o.IsRed() {
			t.Errorf("%q should be red", o)
		}
	}
	for _, o := range []Outcome{Green, GreenWithWarnings, WritingTest, NoDelta} {
		if o.IsRed() {
			t.Errorf("%q should not be red", o)
		}
	}
}
