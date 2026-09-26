package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const nextestNoTestsOutput = tddtest.NextestNoTestsOutput

// TestEmptyPass_NextestZeroTests_NeverBlocksNeverReadsAsFailure pins the
// fix for a real false-positive: `cargo nextest run -p workspace-hack` on a
// dependency-only crate exits 4 ("no tests to run"), which without this
// check made the mechanical stage hard-block the commit as "tests failing"
// and made PostEdit report a RED advisory — over a crate that is NEVER
// supposed to have tests. Both consumers must treat this SuiteResult
// (Passed=false, Err="exit status 4", output naming "no tests to run") as an
// empty PASS.
func TestEmptyPass_NextestZeroTests_NeverBlocksNeverReadsAsFailure(t *testing.T) {
	// Only the SUITE run produces nextest's output; the quality stage runs
	// rustfmt, which has nothing to say about tests.
	nextestZeroTests := func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput}
	}

	t.Run("Precommit mechanical does not block", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
		gitDo(t, root, "add", ".")

		res := Precommit(root, nextestZeroTests)
		if res.Blocked {
			t.Fatalf("a nextest zero-tests exit-4 result must never block, got: %s", res.Message)
		}
	})

	// The half of this contract that CHANGED (2026-09-10): the commit gate
	// must still wave a dependency-only crate through, but PostEdit no
	// longer reports it as a green. A run that executed zero tests said
	// nothing about the code either way, and printing it as green is what
	// let a whole feature's worth of edits read as tested when the narrowed
	// selection was simply empty. Not RED either — nothing failed; the
	// inconclusive family is where it belongs (emptyselection.go).
	t.Run("PostEdit reports the zero-selection line, never red, never green", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := mkProject(t, "Cargo.toml")
		got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), nextestZeroTests)
		wantSub := strings.ToUpper(NoTestsSelected)
		if !strings.Contains(got, wantSub) {
			t.Fatalf("expected the zero-selection line containing %q, got: %s", wantSub, got)
		}
		if strings.Contains(got, "green") {
			t.Fatalf("a run that executed no test must never read as green, got: %s", got)
		}
		if strings.Contains(got, "outcome=red") || strings.Contains(got, "red-") {
			t.Fatalf("a zero-tests nextest exit must never read as RED, got: %s", got)
		}
	})
}

// TestEmptyPass_TableDriven pins treatAsEmptyPass's contract directly: only
// a non-timed-out, non-passing result whose output names nextest's exact
// "no tests to run" failure is treated as an empty pass — a REAL failure
// (any other non-passing output) must still be judged as a failure, or this
// fix would launder actual RED runs into silence.
func TestEmptyPass_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		res  SuiteResult
		want bool
	}{
		{"nextest exit-4 no-tests-to-run", SuiteResult{Passed: false, Output: nextestNoTestsOutput}, true},
		{"already passing is not this case (irrelevant, but must not misfire)", SuiteResult{Passed: true, Output: "ok"}, false},
		{"a real assertion failure", SuiteResult{Passed: false, Output: "--- FAIL: TestWidget\n    want 1 got 2"}, false},
		{"a timed-out run is never reclassified", SuiteResult{Passed: false, TimedOut: true, Output: "no tests to run"}, false},
		{"a compile error mentioning unrelated text", SuiteResult{Passed: false, Output: "error[E0425]: cannot find function `widget`"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := treatAsEmptyPass(c.res); got != c.want {
				t.Fatalf("treatAsEmptyPass(%+v) = %v, want %v", c.res, got, c.want)
			}
		})
	}
}

// TestPrecommit_MechanicalTimeout_IsLoudAndRefuses pins the commit-time
// timeout contract. It used to fail OPEN with a loud FAIL-OPEN/UNVERIFIED
// message; since 2026-09-02 it REFUSES, because the untested code would stay
// in history. What has not changed is that it is never silent, and never
// dressed up as a failing suite.
func TestPrecommit_MechanicalTimeout_IsLoudAndRefuses(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	timedOut := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "suite timed out", TimedOut: true}
	}

	res := Precommit(root, timedOut)
	if !res.Blocked {
		t.Fatal("a commit whose suite never finished must be refused")
	}
	if !strings.Contains(res.Message, "did not finish") || !strings.Contains(res.Message, "retry") {
		t.Fatalf("expected an unfinished/retry message, got: %s", res.Message)
	}
}

// TestFailFirstStage_ThreadsRealDurationIntoLogAndLine pins a real review
// finding: the fail-first stage line and its gate.log entry always showed
// "0.0s" regardless of how long the worktree run actually took — nothing
// threaded SuiteResult.Duration out of failFirstViolatedAt into the log.
// A stub SuiteRunner reporting an 11s Duration must show up as 11.0s in
// BOTH the stderr stage line and the gate.log line, not a hardcoded 0.
func TestFailFirstStage_ThreadsRealDurationIntoLogAndLine(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 { t.Fatal(\"no\") }\n}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	const stubDuration = 11 * time.Second
	// The applied test cannot compile without the staged impl -> a
	// conclusive, non-violating (red-proven) fail-first verdict; with the
	// impl applied the same test passes.
	run := redAtHeadThenGreen(SuiteResult{Passed: false, Output: "./widget_test.go:6:5: undefined: Widget", Duration: stubDuration}, nil)

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})
	if res.Blocked {
		t.Fatalf("expected a conclusive non-violation (red-proven), got blocked: %s", res.Message)
	}
	if !strings.Contains(stderr, "11.0s") {
		t.Fatalf("expected the fail-first stderr line to report the stub's real Duration (11.0s), got: %s", stderr)
	}

	logData, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), " 11.0s") {
		t.Fatalf("expected the fail-first gate.log entry to record the stub's real Duration (11.0s), got:\n%s", logData)
	}
}

// TestFailFirstStage_LogsInconclusiveForInlineRustCfgTest pins the fix for a
// real silent gap: Rust's idiomatic unit-test shape is a `#[test]` inside an
// inline `#[cfg(test)] mod tests { ... }` living in the SAME file as the code
// it exercises — src/widget.rs, never *_test.rs — so ClassifyFile calls it
// Source, never Test, and the len(tests) > 0 gate above never opens for it.
// Before this fix a commit shaped exactly like this ran fail-first NOT AT
// ALL, with nothing in stderr or gate.log to say so. Now it is named.
func TestFailFirstStage_LogsInconclusiveForInlineRustCfgTest(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n\n"+
		"#[cfg(test)]\nmod tests {\n    use super::*;\n\n"+
		"    #[test]\n    fn widget_returns_one() {\n        assert_eq!(widget(), 1);\n    }\n}\n")
	gitDo(t, root, "add", ".")

	run := func(Runner, string) SuiteResult {
		t.Fatal("fail-first must not run a suite for a shape it cannot isolate")
		return SuiteResult{}
	}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStageWithRustNotice(root, root, nil, []string{"src/widget.rs"}, run)
	})
	if res.Blocked {
		t.Fatalf("an unjudgeable shape must never block, got: %s", res.Message)
	}
	if !strings.Contains(stderr, "fail-first") || !strings.Contains(stderr, "inconclusive") {
		t.Fatalf("expected a named, inconclusive fail-first line on stderr, got: %s", stderr)
	}

	logData, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), "inconclusive") {
		t.Fatalf("expected gate.log to record that fail-first looked at this commit, got:\n%s", logData)
	}
}
