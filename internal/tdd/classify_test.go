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
	t.Parallel()
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
	t.Parallel()
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

// ratchet: test_removed TestGoRunIsVacuous_TrueWhenThePackageRanButExecutedNoTest: goRunIsVacuous
// was replaced by vacuousGoPackages (per-package attribution, PR #411
// review); renamed to TestVacuousGoPackages_NamesThePackageThatRanButExecutedNoTest below.
// ratchet: test_removed TestGoRunIsVacuous_FalseWhenNoTestFilesExist: same replacement;
// renamed to TestVacuousGoPackages_EmptyWhenNoTestFilesExist below.
// ratchet: test_removed TestGoRunIsVacuous_FalseWhenTestsActuallyRan: same replacement;
// renamed to TestVacuousGoPackages_EmptyWhenTestsActuallyRan below.

// vacuousPkgJSON is one package's go test -json stream for the #194 shape:
// a TestMain that returns (or calls os.Exit(0)) before m.Run() makes the
// package report a package-level PASS with not one per-test event behind
// it — the same "ok" summary a package with a hundred passing tests would
// print in plain (non -json/-v) text, and the fact vacuousGoPackages exists
// to tell the two apart at commit time.
const vacuousPkgJSON = `{"Action":"start","Package":"example.com/m"}
{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

// noTestFilesPkgJSON is go test's OWN distinction for a package with no
// _test.go files at all: a package-level SKIP, never a PASS, whatever the
// concatenated text says. #317 must not turn every commit touching a
// test-less package into a refusal.
const noTestFilesPkgJSON = `{"Action":"start","Package":"example.com/m"}
{"Action":"output","Package":"example.com/m","Output":"?   \texample.com/m\t[no test files]\n"}
{"Action":"skip","Package":"example.com/m","Elapsed":0}
`

// realTestPkgJSON is the base case: a package whose test genuinely ran and
// passed, reported as a per-test event ahead of the package-level PASS.
const realTestPkgJSON = `{"Action":"run","Package":"example.com/m","Test":"TestWidget"}
{"Action":"output","Package":"example.com/m","Test":"TestWidget","Output":"--- PASS: TestWidget (0.00s)\n"}
{"Action":"pass","Package":"example.com/m","Test":"TestWidget","Elapsed":0}
{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

// TestVacuousGoPackages_NamesThePackageThatRanButExecutedNoTest is the
// single-package case: vacuousGoPackages names the one package whose
// package-level PASS carries no per-test event.
func TestVacuousGoPackages_NamesThePackageThatRanButExecutedNoTest(t *testing.T) {
	t.Parallel()
	got, err := vacuousGoPackages(vacuousPkgJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, []string{"example.com/m"}) {
		t.Fatalf("vacuousGoPackages = %v, want [example.com/m]", got)
	}
}

// TestVacuousGoPackages_EmptyWhenNoTestFilesExist keeps the legitimate
// empty pass (go test's own package-level SKIP) from being caught by the
// same rule as a real vacuous PASS.
func TestVacuousGoPackages_EmptyWhenNoTestFilesExist(t *testing.T) {
	t.Parallel()
	got, err := vacuousGoPackages(noTestFilesPkgJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("vacuousGoPackages = %v, want none (no test files is a legitimate empty pass)", got)
	}
}

// TestVacuousGoPackages_EmptyWhenTestsActuallyRan is the base case: a
// package whose tests really executed must never be flagged.
func TestVacuousGoPackages_EmptyWhenTestsActuallyRan(t *testing.T) {
	t.Parallel()
	got, err := vacuousGoPackages(realTestPkgJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("vacuousGoPackages = %v, want none (a real test ran)", got)
	}
}

// TestVacuousGoPackages_AttributesPerPackageInAMultiPackageRun is the
// review finding on PR #411: judging the vacuous check over a run's WHOLE
// concatenated output made one sibling package's real "--- PASS" line hide
// another package's TestMain that never called m.Run() — exactly #194's
// shape, and exactly the run DetectRunner's Go default (`go test ./...`)
// produces on any repo with more than one package. vacuousGoPackages must
// name pkgvacuous here even though pkgok, in the SAME run, genuinely ran
// and passed a test.
func TestVacuousGoPackages_AttributesPerPackageInAMultiPackageRun(t *testing.T) {
	t.Parallel()
	multiPkgJSON := `{"Action":"start","Package":"multipkg/pkgok"}
{"Action":"start","Package":"multipkg/pkgvacuous"}
{"Action":"output","Package":"multipkg/pkgvacuous","Output":"ok  \tmultipkg/pkgvacuous\t0.087s\n"}
{"Action":"pass","Package":"multipkg/pkgvacuous","Elapsed":0.087}
{"Action":"run","Package":"multipkg/pkgok","Test":"TestOK"}
{"Action":"output","Package":"multipkg/pkgok","Test":"TestOK","Output":"--- PASS: TestOK (0.00s)\n"}
{"Action":"pass","Package":"multipkg/pkgok","Test":"TestOK","Elapsed":0}
{"Action":"output","Package":"multipkg/pkgok","Output":"ok  \tmultipkg/pkgok\t0.104s\n"}
{"Action":"pass","Package":"multipkg/pkgok","Elapsed":0.105}
`
	got, err := vacuousGoPackages(multiPkgJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	want := []string{"multipkg/pkgvacuous"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vacuousGoPackages = %v, want %v — pkgok's real pass must not hide pkgvacuous", got, want)
	}
}

// TestVacuousGoPackages_EmptyStreamIsNotAnError pins the io.EOF exemption:
// an empty GoTestJSON (a non-Go runner, or a stub SuiteResult built without
// setting the field) is a clean zero-event read, never a decode failure.
func TestVacuousGoPackages_EmptyStreamIsNotAnError(t *testing.T) {
	t.Parallel()
	got, err := vacuousGoPackages("")
	if err != nil {
		t.Fatalf("vacuousGoPackages(\"\") error = %v, want nil (an empty stream is a clean end-of-input, not a decode failure)", err)
	}
	if len(got) != 0 {
		t.Fatalf("vacuousGoPackages(\"\") = %v, want none", got)
	}
}

// TestVacuousGoPackages_ReturnsAnErrorOnATruncatedStream is the review
// finding after PR #411's per-package fix: a JSON decode failure partway
// through the stream (a killed process, an interleaved non-JSON write) used
// to be treated identically to a clean end-of-stream (`break` on any error),
// silently under-reporting whatever package's events came after the cut.
// This event is deliberately cut mid-object — no closing brace — so the
// decoder fails with something other than io.EOF.
func TestVacuousGoPackages_ReturnsAnErrorOnATruncatedStream(t *testing.T) {
	t.Parallel()
	truncated := `{"Action":"start","Package":"example.com/m"}
{"Action":"pass","Package":"example.com/m"`
	got, err := vacuousGoPackages(truncated)
	if err == nil {
		t.Fatalf("vacuousGoPackages(truncated) = %v, err = nil — a malformed/truncated stream must be surfaced as an error, not silently read as a clean (possibly empty) result", got)
	}
}

// TestRenderGoTestJSON_PartialParseIsNotPresentedAsComplete is issue #415:
// the sibling function to vacuousGoPackages used to treat ANY decode error
// (including a real one partway through the stream) the same as a clean
// io.EOF end-of-stream, setting ok=true and handing the caller a TRUNCATED
// human-readable reconstruction as though it were the whole run. A failing
// test's "--- FAIL:" line living in an event AFTER the cut would then be
// silently missing from SuiteResult.Output, which ExtractFailingTests and
// the post-edit zeroTestsRe both read. The stream below decodes one full
// event and then ends mid-object (no closing brace) — exactly
// vacuousGoPackages' own truncation fixture — so the decoder fails with
// something other than io.EOF partway through, not at the very start.
func TestRenderGoTestJSON_PartialParseIsNotPresentedAsComplete(t *testing.T) {
	t.Parallel()
	truncated := `{"Action":"output","Package":"example.com/m","Test":"TestFoo","Output":"--- PASS: TestFoo (0.00s)\n"}
{"Action":"output","Package":"example.com/m","Test":"TestBar","Output":"--- FAIL: TestBar (0.00s)\n"`
	human, rawJSON, ok := renderGoTestJSON(truncated)
	if ok {
		t.Fatalf("renderGoTestJSON(truncated) ok = true, want false — a real decode error partway through must not read as a complete reconstruction (got human=%q)", human)
	}
	if human != truncated {
		t.Fatalf("renderGoTestJSON(truncated) human = %q, want the raw stream back as the honest fallback (matching the total-failure path)", human)
	}
	if rawJSON != "" {
		t.Fatalf("renderGoTestJSON(truncated) rawJSON = %q, want empty on !ok (matching the total-failure path)", rawJSON)
	}
}

// TestGoExecArgs_AddsCountEqualsOneAlongsideJSON is issue #421's "-count=1
// belongs everywhere the gate claims to have tested the current tree":
// RunSuite's one seam before every `go test` actually executes must defeat
// go's own test-result cache, or a cached PASS from an earlier tree could
// stand in for a run never made against the one on disk right now — exactly
// as true for the post-edit advisory and the fail-first worktree run as for
// the mechanical suite.
func TestGoExecArgs_AddsCountEqualsOneAlongsideJSON(t *testing.T) {
	t.Parallel()
	got := goExecArgs("go", []string{"test", "./..."})
	want := []string{"test", "-count=1", "-json", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("goExecArgs = %v, want %v", got, want)
	}
}

// TestGoExecArgs_NeverDoublesAFlagAlreadyPresent guards idempotency: a
// second pass over already-armed args (RunSuite only calls this once, but
// nothing enforces that at the type level) must not repeat -json or -count.
func TestGoExecArgs_NeverDoublesAFlagAlreadyPresent(t *testing.T) {
	t.Parallel()
	once := goExecArgs("go", []string{"test", "./..."})
	twice := goExecArgs("go", once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("goExecArgs applied twice = %v, want unchanged %v", twice, once)
	}
}

func TestOutcome_IsRed(t *testing.T) {
	t.Parallel()
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
