package tdd

import (
	"strings"
	"testing"
)

// nextestNoTestsOutput is a representative cargo-nextest transcript for a
// crate with zero test targets (e.g. a cargo-hakari workspace-hack crate,
// which is deliberately dependency-only) — nextest treats this as a hard
// failure and exits 4, unlike plain `cargo test` which exits 0 for the same
// situation.
const nextestNoTestsOutput = "info: auto-detected workspace-hack\n" +
	"    Starting 0 tests across 1 binary\n" +
	"error: no tests to run\n"

// TestEmptyPass_NextestZeroTests_NeverBlocksNeverReadsAsFailure pins the
// fix for a real false-positive: `cargo nextest run -p workspace-hack` on a
// dependency-only crate exits 4 ("no tests to run"), which without this
// check made the mechanical stage hard-block the commit as "tests failing"
// and made PostEdit report a RED advisory — over a crate that is NEVER
// supposed to have tests. Both consumers must treat this SuiteResult
// (Passed=false, Err="exit status 4", output naming "no tests to run") as an
// empty PASS.
func TestEmptyPass_NextestZeroTests_NeverBlocksNeverReadsAsFailure(t *testing.T) {
	nextestZeroTests := func(Runner, string) SuiteResult {
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

// TestPrecommit_MechanicalTimeout_FailOpenMessageNeverSilent pins the A2
// contract: a mechanical-stage timeout fails open (Blocked stays false — a
// stopwatch is not a test verdict) but must NEVER be silent about it. Before
// this task, a timed-out mechanical run returned a bare empty GateResult{}
// indistinguishable from "everything passed"; now the FAIL-OPEN line rides
// in the returned Message even though nothing was rejected.
func TestPrecommit_MechanicalTimeout_FailOpenMessageNeverSilent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	timedOut := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: "suite timed out", TimedOut: true}
	}

	res := Precommit(root, timedOut)
	if res.Blocked {
		t.Fatalf("a timed-out mechanical run must fail OPEN (never block), got blocked: %s", res.Message)
	}
	if res.Message == "" {
		t.Fatal("a timed-out mechanical run must never be silent — Message must be set")
	}
	if !strings.Contains(res.Message, "FAIL-OPEN") || !strings.Contains(res.Message, "UNVERIFIED") {
		t.Fatalf("expected a FAIL-OPEN/UNVERIFIED message, got: %s", res.Message)
	}
}
