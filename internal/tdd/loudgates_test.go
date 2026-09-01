package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nextestNoTestsOutput is VERBATIM cargo-nextest output, captured 2026-08-15
// (review finding: the fixture had been hand-written; this replaces it with
// a real capture) via:
//
//	cargo new --lib zz_empty && cd zz_empty
//	# src/lib.rs stripped of its default #[test] so the crate has ZERO tests
//	cargo nextest run
//
// which exits 4 (confirmed: `echo $?` => 4) with this transcript — the exact
// shape a cargo-hakari workspace-hack crate (deliberately dependency-only)
// produces on every commit that touches it, unlike plain `cargo test` (also
// captured, same zero-test crate: exit 0, "running 0 tests" /
// "test result: ok. 0 passed; 0 failed; ..." — already covered by
// ClassifyOutcome's existing zeroTestsRe, so no fixture change needed there).
const nextestNoTestsOutput = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.01s\n" +
	"────────────\n" +
	" Nextest run ID 2c75af04-dbe9-4f97-aa96-b51d23e40db1 with nextest profile: default\n" +
	"    Starting 0 tests across 1 binary\n" +
	"────────────\n" +
	"     Summary [   0.000s] 0 tests run: 0 passed, 0 skipped\n" +
	"error: no tests to run\n" +
	"(hint: use `--no-tests` to customize)\n"

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

	t.Run("PostEdit reports the green-empty line, never red", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := mkProject(t, "Cargo.toml")
		got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), nextestZeroTests)
		wantSub := "green (0 tests — nothing to run"
		if !strings.Contains(got, wantSub) {
			t.Fatalf("expected the green-empty line containing %q, got: %s", wantSub, got)
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
	run := func(Runner, string) SuiteResult {
		// The applied test cannot compile without the staged impl -> a
		// conclusive, non-violating (red-proven) fail-first verdict,
		// regardless of which directory this stub is invoked in.
		return SuiteResult{Passed: false, Output: "undefined: Widget", Duration: stubDuration}
	}

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

	logData, err := os.ReadFile(filepath.Join(cfg, "tdd-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), " 11.0s") {
		t.Fatalf("expected the fail-first gate.log entry to record the stub's real Duration (11.0s), got:\n%s", logData)
	}
}
