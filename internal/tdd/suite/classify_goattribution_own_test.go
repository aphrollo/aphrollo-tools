package suite

import (
	"encoding/json"
	"testing"
)

// These are suite's own tests of classify_goattribution.go, classify_scope.go's
// scoping entry points and classify_attribution.go's attribution, reached today
// only through internal/tdd/postedit and precommit.

const (
	jsonPkgPass = `{"Action":"pass","Package":"example.com/m","Elapsed":0.01}` + "\n"
	jsonTestRun = `{"Action":"pass","Package":"example.com/m","Test":"TestA","Elapsed":0}` + "\n"
)

// jsonPkgOutput is a package-level output event carrying text, the way go test
// -json prints what the package itself wrote.
func jsonPkgOutput(text string) string {
	b, _ := json.Marshal(map[string]string{"Action": "output", "Package": "example.com/m", "Output": text})
	return string(b) + "\n"
}

// TestGoJSONRanATest_APerTestEventMeansATestRan pins the event reading: a
// pass, fail or skip event that names a test is a test having run.
func TestGoJSONRanATest_APerTestEventMeansATestRan(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"pass", "fail", "skip"} {
		raw := `{"Action":"` + action + `","Package":"p","Test":"TestA"}` + "\n"
		if ran, known := goJSONRanATest(raw); !ran || !known {
			t.Errorf("action %q: (ran, known) = (%v, %v), want (true, true)", action, ran, known)
		}
	}
}

// TestGoJSONRanATest_APackageVerdictAloneRanNothing pins the #194 shape: the
// package passed and no test event stands behind it. It is known, and empty.
func TestGoJSONRanATest_APackageVerdictAloneRanNothing(t *testing.T) {
	t.Parallel()
	if ran, known := goJSONRanATest(jsonPkgPass); ran || !known {
		t.Fatalf("(ran, known) = (%v, %v), want (false, true)", ran, known)
	}
}

// TestGoJSONRanATest_ARunOrOutputEventDoesNotCountAsATestHavingRun pins that
// only a settled per-test action counts: run and output events for a test are
// not evidence it finished.
func TestGoJSONRanATest_ARunOrOutputEventDoesNotCountAsATestHavingRun(t *testing.T) {
	t.Parallel()
	raw := `{"Action":"run","Package":"p","Test":"TestA"}` + "\n" +
		`{"Action":"output","Package":"p","Test":"TestA","Output":"x"}` + "\n"
	if ran, known := goJSONRanATest(raw); ran || !known {
		t.Fatalf("(ran, known) = (%v, %v), want (false, true)", ran, known)
	}
}

// TestGoJSONRanATest_EventsWithoutAPackageAreNotEvidence pins that a stream of
// events naming no package (build noise) leaves the answer unknown.
func TestGoJSONRanATest_EventsWithoutAPackageAreNotEvidence(t *testing.T) {
	t.Parallel()
	if ran, known := goJSONRanATest(`{"Action":"start"}` + "\n"); ran || known {
		t.Fatalf("(ran, known) = (%v, %v), want (false, false)", ran, known)
	}
}

// TestGoJSONRanATest_ATornStreamIsUnknown pins the decode-error arm: a stream
// that stops mid-event says nothing, even after a test event.
func TestGoJSONRanATest_ATornStreamIsUnknown(t *testing.T) {
	t.Parallel()
	if ran, known := goJSONRanATest(jsonTestRun + `{"Action":"pa`); ran || known {
		t.Fatalf("(ran, known) = (%v, %v), want (false, false)", ran, known)
	}
}

// TestGoRunRanATest_PrefersTheJSONStreamOverTheText pins the precedence: with
// a JSON stream the text is ignored.
func TestGoRunRanATest_PrefersTheJSONStreamOverTheText(t *testing.T) {
	t.Parallel()
	res := SuiteResult{GoTestJSON: jsonPkgPass, Output: "ok  \texample.com/m\t0.01s\n"}
	if ran, known := goRunRanATest(res); ran || !known {
		t.Fatalf("(ran, known) = (%v, %v), want the empty JSON verdict (false, true)", ran, known)
	}
}

// TestGoRunRanATest_ReadsPackageLinesWhenThereIsNoJSON pins the text fallback:
// an `ok` package line that is not a no-tests line means tests ran.
func TestGoRunRanATest_ReadsPackageLinesWhenThereIsNoJSON(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Output: "?   \texample.com/a\t[no test files]\nok  \texample.com/b\t0.02s\n"}
	if ran, known := goRunRanATest(res); !ran || !known {
		t.Fatalf("(ran, known) = (%v, %v), want (true, true)", ran, known)
	}
}

// TestGoRunRanATest_NoTestsToRunAndNoTestFilesRanNothing pins the two package
// lines that look like passes and are not.
func TestGoRunRanATest_NoTestsToRunAndNoTestFilesRanNothing(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Output: "?   \texample.com/a\t[no test files]\nok  \texample.com/b\t0.02s [no tests to run]\n"}
	if ran, known := goRunRanATest(res); ran || !known {
		t.Fatalf("(ran, known) = (%v, %v), want (false, true)", ran, known)
	}
}

// TestGoRunRanATest_NoPackageLinesIsUnknown pins the empty-evidence arm.
func TestGoRunRanATest_NoPackageLinesIsUnknown(t *testing.T) {
	t.Parallel()
	if ran, known := goRunRanATest(SuiteResult{Output: "building...\n"}); ran || known {
		t.Fatalf("(ran, known) = (%v, %v), want (false, false)", ran, known)
	}
}

// TestGoPassedCount_CountsTopLevelPassLinesOnly pins the count and its ok
// flag: subtests (indented) are not counted, and zero is not ok.
func TestGoPassedCount_CountsTopLevelPassLinesOnly(t *testing.T) {
	t.Parallel()
	n, ok := goPassedCount("--- PASS: TestA (0.00s)\n    --- PASS: TestA/sub (0.00s)\n--- PASS: TestB (0.00s)\n")
	if n != 2 || !ok {
		t.Fatalf("goPassedCount = (%d, %v), want (2, true)", n, ok)
	}
	if n, ok := goPassedCount("--- FAIL: TestA (0.00s)\n"); n != 0 || ok {
		t.Fatalf("goPassedCount = (%d, %v), want (0, false)", n, ok)
	}
}

// TestClassifyRunOutcome_AGoRunThatRanNothingIsWritingATest pins the passing
// go arm: a green run with no test behind it is a test being written, not a
// green.
func TestClassifyRunOutcome_AGoRunThatRanNothingIsWritingATest(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Passed: true, GoTestJSON: jsonPkgPass}
	if got := classifyRunOutcome(Runner{Cmd: "go"}, t.TempDir(), res, nil); got != WritingTest {
		t.Fatalf("outcome = %v, want WritingTest", got)
	}
}

// TestClassifyRunOutcome_AGoRunThatRanATestIsGreen pins the plain green.
func TestClassifyRunOutcome_AGoRunThatRanATestIsGreen(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Passed: true, GoTestJSON: jsonTestRun, Output: "ok  \texample.com/m\t0.01s\n"}
	if got := classifyRunOutcome(Runner{Cmd: "go"}, t.TempDir(), res, nil); got != Green {
		t.Fatalf("outcome = %v, want Green", got)
	}
}

// TestClassifyRunOutcome_AGreenGoRunWithAWarningSaysSo pins the warning arm.
func TestClassifyRunOutcome_AGreenGoRunWithAWarningSaysSo(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Passed: true, GoTestJSON: jsonPkgOutput("warning: something is deprecated\n") + jsonTestRun}
	if got := classifyRunOutcome(Runner{Cmd: "go"}, t.TempDir(), res, nil); got != GreenWithWarnings {
		t.Fatalf("outcome = %v, want GreenWithWarnings", got)
	}
}

// TestClassifyRunOutcome_TheNoTestsNoteIsNotAWarning pins that go's own
// "testing: warning: no tests to run" note is stripped before the warning
// check, so a run that ran a test elsewhere and printed it stays plain green.
func TestClassifyRunOutcome_TheNoTestsNoteIsNotAWarning(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Passed: true, GoTestJSON: jsonPkgOutput("testing: warning: no tests to run\n") + jsonTestRun}
	if got := classifyRunOutcome(Runner{Cmd: "go"}, t.TempDir(), res, nil); got != Green {
		t.Fatalf("outcome = %v, want Green", got)
	}
}

// TestClassifyRunOutcome_ARunWithNoGoVerdictFallsBackToTheTextClassifier pins
// the fall-through: a go run whose verdict is unknown, and any non-go runner,
// go through ClassifyOutcome.
func TestClassifyRunOutcome_ARunWithNoGoVerdictFallsBackToTheTextClassifier(t *testing.T) {
	t.Parallel()
	if got := classifyRunOutcome(Runner{Cmd: "go"}, t.TempDir(), SuiteResult{Passed: true, Output: "building...\n"}, nil); got != Green {
		t.Fatalf("go with no verdict: outcome = %v, want Green from the text classifier", got)
	}
	if got := classifyRunOutcome(Runner{Cmd: "pytest"}, t.TempDir(), SuiteResult{Passed: true, Output: "1 passed\n"}, nil); got != Green {
		t.Fatalf("pytest: outcome = %v, want Green", got)
	}
}

// TestClassificationOutput_ScopesToTheFailuresOfAValidStream pins the scoping
// entry point: with a decodable stream only unattributed output and a failing
// test's output are kept.
func TestClassificationOutput_ScopesToTheFailuresOfAValidStream(t *testing.T) {
	t.Parallel()
	raw := `{"Action":"output","Package":"p","Test":"TestPass","Output":"undefined: Fixture\n"}` + "\n" +
		`{"Action":"pass","Package":"p","Test":"TestPass"}` + "\n" +
		`{"Action":"output","Package":"p","Test":"TestBad","Output":"boom\n"}` + "\n" +
		`{"Action":"fail","Package":"p","Test":"TestBad"}` + "\n" +
		`{"Action":"build-output","Package":"p","Output":"pkg-level\n"}` + "\n"
	got := classificationOutput("flat text", raw)
	if got != "boom\npkg-level\n" {
		t.Fatalf("classificationOutput = %q, want only the failing test's and the package-level output", got)
	}
}

// TestClassificationOutput_FallsBackToTheFlatTextWithoutAStream pins the other
// half: no stream, or one that does not decode, leaves the text untouched.
func TestClassificationOutput_FallsBackToTheFlatTextWithoutAStream(t *testing.T) {
	t.Parallel()
	if got := classificationOutput("flat text", ""); got != "flat text" {
		t.Fatalf("no stream: %q, want the flat text", got)
	}
	if got := classificationOutput("flat text", "not json"); got != "flat text" {
		t.Fatalf("bad stream: %q, want the flat text", got)
	}
}

// TestGoUndefinedIsMissingImportOnly_EveryNameAStdlibPackageIsAMissingImport
// pins the positive case and its guards: only standard-library names, no
// undefined lines at all, and a mix.
func TestGoUndefinedIsMissingImportOnly_EveryNameAStdlibPackageIsAMissingImport(t *testing.T) {
	t.Parallel()
	if !goUndefinedIsMissingImportOnly("a_test.go:3:2: undefined: strings\na_test.go:4:2: undefined: os\n") {
		t.Fatal("undefined: strings and os are missing imports")
	}
	if goUndefinedIsMissingImportOnly("no diagnostics here\n") {
		t.Fatal("no undefined line at all is not a missing-import verdict")
	}
	if goUndefinedIsMissingImportOnly("a_test.go:3:2: undefined: strings\na_test.go:4:2: undefined: Widget\n") {
		t.Fatal("one non-package name keeps the missing-impl claim")
	}
}

// TestAttributeMissingImpl_OnlyAMissingImplVerdictOnAnAttributedToolchainIsChecked
// pins the pass-through: every other outcome, and every other runner, come
// back unchanged without reading the output.
func TestAttributeMissingImpl_OnlyAMissingImplVerdictOnAnAttributedToolchainIsChecked(t *testing.T) {
	t.Parallel()
	if got := attributeMissingImpl(Green, Runner{Cmd: "go"}, "/w", "undefined: X"); got != Green {
		t.Errorf("Green passes through, got %v", got)
	}
	if got := attributeMissingImpl(RedMissingImpl, Runner{Cmd: "pytest"}, "/w", "undefined: X"); got != RedMissingImpl {
		t.Errorf("pytest is not attributed, got %v", got)
	}
}

// TestAttributeMissingImpl_AnUnlocatedMissingSymbolIsAnOrdinaryRed pins the
// downgrade: an attributed toolchain whose missing-symbol phrase has no
// location in test code is Red, and one located in a test file stays
// RedMissingImpl.
func TestAttributeMissingImpl_AnUnlocatedMissingSymbolIsAnOrdinaryRed(t *testing.T) {
	t.Parallel()
	if got := attributeMissingImpl(RedMissingImpl, Runner{Cmd: "go"}, t.TempDir(), "    log line says undefined: Widget\n"); got != Red {
		t.Errorf("unlocated phrase: %v, want Red", got)
	}
	if got := attributeMissingImpl(RedMissingImpl, Runner{Cmd: "go"}, t.TempDir(), "widget_test.go:9:3: undefined: Widget\n"); got != RedMissingImpl {
		t.Errorf("located in a test file: %v, want RedMissingImpl", got)
	}
}

// TestMissingSymbolInTestCode_ALocationInSourceCodeIsNotTestCode pins that a
// diagnostic located in a non-test source file does not count.
func TestMissingSymbolInTestCode_ALocationInSourceCodeIsNotTestCode(t *testing.T) {
	t.Parallel()
	if missingSymbolInTestCode("widget.go:9:3: undefined: Widget\n", t.TempDir()) {
		t.Fatal("widget.go is not test code")
	}
}

// TestMissingSymbolInTestCode_RustcLocationsSitOnTheLineBelowTheHeader pins the
// rustc shape: the header line carries the phrase and the `-->` line below it
// carries the location.
func TestMissingSymbolInTestCode_RustcLocationsSitOnTheLineBelowTheHeader(t *testing.T) {
	t.Parallel()
	out := "error[E0425]: cannot find function `widget` in this scope\n --> tests/widget.rs:4:5\n"
	if !missingSymbolInTestCode(out, t.TempDir()) {
		t.Fatal("a rustc diagnostic located in tests/widget.rs is in test code")
	}
	noPhrase := "error[E0308]: mismatched types\n --> tests/widget.rs:4:5\n"
	if missingSymbolInTestCode(noPhrase, t.TempDir()) {
		t.Fatal("a `-->` line under a header with no missing-symbol phrase does not count")
	}
}

// TestIsTestCodeAt_ARustLineInsideACfgTestModuleIsTestCode pins the on-disk
// read: a src file's #[cfg(test)] module lines are test code, and the lines
// above it are not.
func TestIsTestCodeAt_ARustLineInsideACfgTestModuleIsTestCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/lib.rs", "pub fn widget() {}\n\n#[cfg(test)]\nmod tests {\n    fn t() { missing(); }\n}\n")
	if !isTestCodeAt("src/lib.rs", 5, dir) {
		t.Error("line 5 sits inside the #[cfg(test)] module")
	}
	if isTestCodeAt("src/lib.rs", 1, dir) {
		t.Error("line 1 is production code")
	}
}

// TestIsTestCodeAt_UnreadableOrNonRustIsNotProvablyTestCode pins the two
// arms that answer false: a non-Rust non-test file, and a Rust file that is
// not on disk.
func TestIsTestCodeAt_UnreadableOrNonRustIsNotProvablyTestCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if isTestCodeAt("widget.go", 1, dir) {
		t.Error("widget.go is not test code")
	}
	if isTestCodeAt("src/gone.rs", 1, dir) {
		t.Error("a Rust file that cannot be read is not provably test code")
	}
}

// TestIsTestCodeAt_AnAbsoluteRustPathIsReadAsGiven pins that an absolute
// location is not joined onto the run dir.
func TestIsTestCodeAt_AnAbsoluteRustPathIsReadAsGiven(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/lib.rs", "#[cfg(test)]\nmod tests {\n    fn t() {}\n}\n")
	abs := dir + "/src/lib.rs"
	if !isTestCodeAt(abs, 3, "/nonexistent-run-dir") {
		t.Fatal("an absolute path must be read as-is")
	}
}
