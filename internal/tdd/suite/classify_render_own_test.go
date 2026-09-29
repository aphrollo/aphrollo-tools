package suite

import "testing"

// These are suite's own tests of classify.go's go-test JSON rendering and of
// the ClassifyOutcome arm that treats a broken import as a bogus red, reached
// today only through internal/tdd/postedit and precommit.

// TestRenderGoTestJSON_ConcatenatesOutputAndBuildOutputInStreamOrder pins the
// reconstruction: only output and build-output events contribute, in order,
// and the raw stream comes back untouched beside it.
func TestRenderGoTestJSON_ConcatenatesOutputAndBuildOutputInStreamOrder(t *testing.T) {
	t.Parallel()
	raw := `{"Action":"start","Package":"p"}` + "\n" +
		`{"Action":"output","Package":"p","Output":"one\n"}` + "\n" +
		`{"Action":"pass","Package":"p","Test":"TestA"}` + "\n" +
		`{"Action":"build-output","Package":"p","Output":"two\n"}` + "\n"
	human, rawOut, ok := renderGoTestJSON(raw)
	if !ok || human != "one\ntwo\n" || rawOut != raw {
		t.Fatalf("renderGoTestJSON = (%q, raw-equal %v, %v), want (\"one\\ntwo\\n\", true, true)", human, rawOut == raw, ok)
	}
}

// TestRenderGoTestJSON_ATornStreamFallsBackToTheRawText pins the honest
// fallback: a decode error partway through means the reconstruction would be
// partial, so the raw stream is returned as the text and ok is false.
func TestRenderGoTestJSON_ATornStreamFallsBackToTheRawText(t *testing.T) {
	t.Parallel()
	raw := `{"Action":"output","Package":"p","Output":"kept?\n"}` + "\n" + `{"Action":"out`
	human, rawOut, ok := renderGoTestJSON(raw)
	if ok || human != raw || rawOut != "" {
		t.Fatalf("renderGoTestJSON = (%q, %q, %v), want the raw text back with ok=false", human, rawOut, ok)
	}
}

// TestRenderGoTestJSON_NoRecognisableEventIsNotAStream pins the empty arm:
// empty or whitespace-only input carries no event at all.
func TestRenderGoTestJSON_NoRecognisableEventIsNotAStream(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "  \n"} {
		if human, rawOut, ok := renderGoTestJSON(raw); ok || human != raw || rawOut != "" {
			t.Errorf("renderGoTestJSON(%q) = (%q, %q, %v), want raw back with ok=false", raw, human, rawOut, ok)
		}
	}
}

// TestGoRenderedOutput_RendersOnlyAGoTestInvocation pins the dispatch: a
// `go test` run is rendered from its JSON, anything else passes straight
// through with no JSON, and a go test stream that cannot be parsed falls back
// to the raw text.
func TestGoRenderedOutput_RendersOnlyAGoTestInvocation(t *testing.T) {
	t.Parallel()
	raw := `{"Action":"output","Package":"p","Output":"hello\n"}` + "\n"
	out, js := goRenderedOutput("go", []string{"test", "-json", "./..."}, raw)
	if out != "hello\n" || js != raw {
		t.Errorf("go test: (%q, %q), want the rendered text and the raw JSON", out, js)
	}
	if out, js := goRenderedOutput("cargo", []string{"test"}, raw); out != raw || js != "" {
		t.Errorf("cargo: (%q, %q), want the raw text and no JSON", out, js)
	}
	if out, js := goRenderedOutput("go", []string{"build"}, raw); out != raw || js != "" {
		t.Errorf("go build: (%q, %q), want the raw text and no JSON", out, js)
	}
	if out, js := goRenderedOutput("go", []string{"test"}, "not json"); out != "not json" || js != "" {
		t.Errorf("garbled go test: (%q, %q), want the raw text and no JSON", out, js)
	}
}

// TestClassifyOutcome_AMissingImportIsABrokenTestFileNotAMissingImpl pins the
// arm ClassifyOutcome reaches through goUndefinedIsMissingImportOnly: every
// undefined name is a stdlib package, so the test file forgot an import and
// the red is bogus, not a clean RED for missing code.
func TestClassifyOutcome_AMissingImportIsABrokenTestFileNotAMissingImpl(t *testing.T) {
	t.Parallel()
	got := ClassifyOutcome(false, "a_test.go:3:2: undefined: strings\n", nil)
	if got != RedBogus {
		t.Fatalf("ClassifyOutcome = %v, want RedBogus", got)
	}
	got = ClassifyOutcome(false, "a_test.go:3:2: undefined: Widget\n", nil)
	if got != RedMissingImpl {
		t.Fatalf("ClassifyOutcome = %v, want RedMissingImpl for a non-package name", got)
	}
}

// TestVacuousNames_DispatchesOnTheRunnersToolchain pins each arm of the one
// dispatch point runSuiteStage and fail-first share.
func TestVacuousNames_DispatchesOnTheRunnersToolchain(t *testing.T) {
	t.Parallel()
	got, err := vacuousNames(Runner{Cmd: "go"}, SuiteResult{GoTestJSON: jsonPkgPass})
	if err != nil || len(got) != 1 || got[0] != "example.com/m" {
		t.Errorf("go: (%v, %v), want the empty package named", got, err)
	}
	cargoOut := "     Running unittests src/lib.rs (x)\n" + cargoZeroFiltered
	if got, err := vacuousNames(Runner{Cmd: "cargo"}, SuiteResult{Output: cargoOut}); err != nil || len(got) != 1 || got[0] != "src/lib.rs" {
		t.Errorf("cargo: (%v, %v), want src/lib.rs", got, err)
	}
	if got, err := vacuousNames(Runner{Cmd: "pytest"}, SuiteResult{Output: "\n3 deselected in 0.00s\n"}); err != nil || len(got) != 1 || got[0] != "pytest" {
		t.Errorf("pytest vacuous: (%v, %v), want [pytest]", got, err)
	}
	if got, err := vacuousNames(Runner{Cmd: "pytest"}, SuiteResult{Output: "2 passed in 0.01s\n"}); err != nil || got != nil {
		t.Errorf("pytest real: (%v, %v), want nothing", got, err)
	}
	vitest := Runner{Cmd: "npx", Args: []string{"vitest", "run"}}
	if got, err := vacuousNames(vitest, SuiteResult{Output: " Test Files  1 passed (1)\n      Tests  no tests (0)\n"}); err != nil || len(got) != 1 || got[0] != "vitest" {
		t.Errorf("vitest vacuous: (%v, %v), want [vitest]", got, err)
	}
	if got, err := vacuousNames(vitest, SuiteResult{Output: " Test Files  1 passed (1)\n      Tests  3 passed (3)\n"}); err != nil || got != nil {
		t.Errorf("vitest real: (%v, %v), want nothing", got, err)
	}
	if got, err := vacuousNames(Runner{Cmd: "zig"}, SuiteResult{Output: "x"}); err != nil || got != nil {
		t.Errorf("unknown toolchain: (%v, %v), want nothing", got, err)
	}
}

// TestVacuousNames_AGoStreamThatCannotBeReadIsAnError pins the one arm that can
// fail: a torn go test -json stream is an error, not "no vacuous packages".
func TestVacuousNames_AGoStreamThatCannotBeReadIsAnError(t *testing.T) {
	t.Parallel()
	_, err := vacuousNames(Runner{Cmd: "go"}, SuiteResult{GoTestJSON: jsonPkgPass + `{"Action":"pa`})
	if err == nil {
		t.Fatal("a torn go test -json stream must be an error, not an empty verdict")
	}
}
