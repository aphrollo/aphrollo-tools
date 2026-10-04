package suite

import (
	"strings"
	"testing"
	"time"
)

// These are suite's own tests of buildonly.go, reached today only through
// internal/tdd/postedit and precommit.

// TestBuildOnlyRunner_ExampleAndBenchAreCompileChecks pins the two flags, in
// either spelling of a value.
func TestBuildOnlyRunner_ExampleAndBenchAreCompileChecks(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"test", "--example", "demo"},
		{"test", "--bench", "speed"},
		{"test", "--bench=speed"},
	} {
		if !buildOnlyRunner(Runner{Cmd: "cargo", Args: args}) {
			t.Errorf("buildOnlyRunner(cargo %v) = false, want true", args)
		}
	}
}

// TestBuildOnlyRunner_OrdinaryTestRunsAndOtherToolsAreNot pins the negatives.
func TestBuildOnlyRunner_OrdinaryTestRunsAndOtherToolsAreNot(t *testing.T) {
	t.Parallel()
	if buildOnlyRunner(Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}) {
		t.Error("an ordinary cargo test is not a compile check")
	}
	if buildOnlyRunner(Runner{Cmd: "go", Args: []string{"test", "--example"}}) {
		t.Error("only cargo has build-only targets")
	}
}

// TestUntestedVerdict_NamesWhatANonFailingRunProvedNothingAbout pins the
// three answers: a passing compile check is build-only, an empty selection is
// no-tests-selected, and a run that executed tests is neither.
func TestUntestedVerdict_NamesWhatANonFailingRunProvedNothingAbout(t *testing.T) {
	t.Parallel()
	example := Runner{Cmd: "cargo", Args: []string{"test", "--example", "demo"}}
	if got := untestedVerdict(example, SuiteResult{Passed: true}); got != BuildOnly {
		t.Errorf("passing compile check: %q, want %q", got, BuildOnly)
	}
	plain := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}
	if got := untestedVerdict(plain, SuiteResult{Passed: true, Output: libtestZero}); got != NoTestsSelected {
		t.Errorf("empty selection: %q, want %q", got, NoTestsSelected)
	}
	if got := untestedVerdict(plain, SuiteResult{Passed: true, Output: libtestPassed}); got != "" {
		t.Errorf("a run that executed tests: %q, want empty", got)
	}
}

// TestUntestedVerdict_ACompileCheckThatFailedIsARealRed pins that a build-only
// target that did not compile is not laundered into an inconclusive verdict.
func TestUntestedVerdict_ACompileCheckThatFailedIsARealRed(t *testing.T) {
	t.Parallel()
	example := Runner{Cmd: "cargo", Args: []string{"test", "--example", "demo"}}
	if got := untestedVerdict(example, SuiteResult{Passed: false}); got != "" {
		t.Fatalf("untestedVerdict = %q, want empty for a failed compile", got)
	}
}

// TestBuildOnlyTargetKind_NamesTheFlagItSaw pins the label: the flag's own
// name, without its value, or the generic fallback.
func TestBuildOnlyTargetKind_NamesTheFlagItSaw(t *testing.T) {
	t.Parallel()
	if got := buildOnlyTargetKind(Runner{Args: []string{"test", "--bench=speed"}}); got != "--bench" {
		t.Errorf("kind = %q, want --bench", got)
	}
	if got := buildOnlyTargetKind(Runner{Args: []string{"test", "--example", "demo"}}); got != "--example" {
		t.Errorf("kind = %q, want --example", got)
	}
	if got := buildOnlyTargetKind(Runner{Args: []string{"test"}}); got != "a build-only" {
		t.Errorf("kind = %q, want the generic fallback", got)
	}
}

// TestBuildOnlyAdvisory_SaysItWasACompileCheckAndNothingWasTested pins the
// line's content.
func TestBuildOnlyAdvisory_SaysItWasACompileCheckAndNothingWasTested(t *testing.T) {
	t.Parallel()
	r := Runner{Cmd: "cargo", Args: []string{"test", "--example", "demo"}}
	got := buildOnlyAdvisory(r, "/w", 1500*time.Millisecond)
	for _, part := range []string{"cargo test --example demo", "in /w", "BUILD-ONLY", "1.5s", "an --example target", "NOT tested"} {
		if !strings.Contains(got, part) {
			t.Fatalf("advisory %q lacks %q", got, part)
		}
	}
}

// Serial: appends to gate.log under its own CLAUDE_CONFIG_DIR, a process-wide env var.
// TestBuildOnlyTerminal_LogsAndRendersACleanCompileCheck pins the terminal
// line for a clean compile check: it is rendered and its verdict logged.
func TestBuildOnlyTerminal_LogsAndRendersACleanCompileCheck(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	r := Runner{Cmd: "cargo", Args: []string{"test", "--example", "demo"}}
	got := buildOnlyTerminal(r, t.TempDir(), SuiteResult{Passed: true, Duration: time.Second})
	if !strings.Contains(got, "BUILD-ONLY") {
		t.Fatalf("terminal = %q, want the build-only advisory", got)
	}
	requireLoggedVerdict(t, cfg, BuildOnly)
}

// TestBuildOnlyTerminal_ARunThatIsNotACompileCheckHasNoTerminalLine pins the
// empty answer.
func TestBuildOnlyTerminal_ARunThatIsNotACompileCheckHasNoTerminalLine(t *testing.T) {
	t.Parallel()
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}
	if got := buildOnlyTerminal(r, t.TempDir(), SuiteResult{Passed: true, Output: libtestPassed}); got != "" {
		t.Fatalf("terminal = %q, want none", got)
	}
}

// TestHasNoRunFlag_OnlyTheExactFlagCounts pins the argv scan: the exact
// --no-run token, not a prefix of it and not a value that contains it.
func TestHasNoRunFlag_OnlyTheExactFlagCounts(t *testing.T) {
	t.Parallel()
	if !hasNoRunFlag([]string{"nextest", "run", "--no-run"}) {
		t.Error("--no-run present must be found")
	}
	if hasNoRunFlag([]string{"nextest", "run", "--no-run-extra", "x--no-run"}) {
		t.Error("only the exact token counts")
	}
	if hasNoRunFlag(nil) {
		t.Error("no argv, no flag")
	}
}
