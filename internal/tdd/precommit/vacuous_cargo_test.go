package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every fixture below is captured verbatim from a real `cargo test` run
// against a throwaway crate on this box (Windows paths and all), not
// invented — the ground truth cargoVacuousTargets' own doc comment cites.

// cargoRealPassOutput: a lib target with one real passing test, an
// integration test target with one real passing test, and doc-tests that
// genuinely have none.
const cargoRealPassOutput = `   Compiling vactest v0.1.0 (C:\repo)
    Finished ` + "`test`" + ` profile [unoptimized + debuginfo] target(s) in 1.18s
     Running unittests src\lib.rs (target\debug\deps\vactest-a2202e17fc8b0ca6.exe)

running 2 tests
test tests::ignored_one ... ignored
test tests::passes ... ok

test result: ok. 1 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s

     Running tests\it.rs (target\debug\deps\it-0f4d0c5f4366a998.exe)

running 1 test
test it_works ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

   Doc-tests vactest

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
`

// TestCargoVacuousTargets_EmptyWhenRealTestsRanAndDocTestsGenuinelyHaveNone
// is the base case: a real passing test, an ignored one beside it (skipped
// is not the same as not executed — #412), and a doc-tests block with
// genuinely zero of everything (0 filtered out) must never be flagged.
func TestCargoVacuousTargets_EmptyWhenRealTestsRanAndDocTestsGenuinelyHaveNone(t *testing.T) {
	if got := cargoVacuousTargets(cargoRealPassOutput); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none — one target ran a real test, the other two legitimately have none", got)
	}
}

// cargoFilterMismatchOutput: `cargo test nomatch` against a target whose
// real tests exist but none match the given filter — captured verbatim,
// filtered_out nonzero.
const cargoFilterMismatchOutput = `     Running unittests src\lib.rs (target\debug\deps\ui-4c443b6e7abff3fe.exe)

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 109 filtered out; finished in 0.00s

     Running tests\integration\main.rs (target\debug\deps\integration-6748a9ea55b287a2.exe)

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 312 filtered out; finished in 0.00s
`

// TestCargoVacuousTargets_NamesTargetsWhoseRealTestsWereAllFilteredOut is
// #469's cargo counterpart to vacuousGoPackages' #194 case: a name/filter
// mismatch (vacuousFailFirstMessage's own stated cause) excluded every real
// test in BOTH targets, distinguishable from "genuinely no tests" only by
// filtered_out being nonzero.
func TestCargoVacuousTargets_NamesTargetsWhoseRealTestsWereAllFilteredOut(t *testing.T) {
	got := cargoVacuousTargets(cargoFilterMismatchOutput)
	want := []string{`src\lib.rs`, `tests\integration\main.rs`}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cargoVacuousTargets = %v, want %v", got, want)
	}
}

// TestCargoVacuousTargets_EmptyOnAPlainCompileFailure is the "must not
// relabel a build failure as vacuous" fixture #412 asks for: a compile error
// prints no "test result:" line at all, so the parser must find nothing to
// flag (the caller's own res.Passed gate is what actually keeps a failed
// build from ever reaching this function, but the parser itself must not
// manufacture a false vacuous reading if it ever were handed this text).
func TestCargoVacuousTargets_EmptyOnAPlainCompileFailure(t *testing.T) {
	const compileFailure = `error[E0425]: cannot find value ` + "`x`" + ` in this scope
 --> src/lib.rs:3:5
  |
3 |     x
  |     ^ not found in this scope

error: could not compile ` + "`vactest`" + ` (lib test) due to 1 previous error
`
	if got := cargoVacuousTargets(compileFailure); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none — a compile failure prints no test result line to misread", got)
	}
}

// TestCargoVacuousTargets_IgnoredAloneIsNotVacuous: a target whose tests are
// ALL #[ignore]d, with no filter involved (filtered_out == 0), must not be
// flagged — libtest itself decided to skip them, which counts as executed
// (constraint #2).
func TestCargoVacuousTargets_IgnoredAloneIsNotVacuous(t *testing.T) {
	const allIgnored = `     Running unittests src\lib.rs (target\debug\deps\vactest-a2202e17fc8b0ca6.exe)

running 1 test
test tests::slow ... ignored

test result: ok. 0 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s
`
	if got := cargoVacuousTargets(allIgnored); len(got) != 0 {
		t.Fatalf("cargoVacuousTargets = %v, want none — every test present was ignored, not excluded by a filter", got)
	}
}

// TestPrecommit_RejectsACargoSuiteThatExecutedZeroTests pins the production
// wiring, mirroring TestPrecommit_RejectsAGoSuiteThatExecutedZeroTests for
// cargo: a mechanical run whose libtest summary shows every real test
// filtered out must block the commit with the vacuous-rejected token, not
// pass because the process happened to exit 0.
func TestPrecommit_RejectsACargoSuiteThatExecutedZeroTests(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(r Runner, _ string) SuiteResult {
		return SuiteResult{Passed: true, Output: cargoFilterMismatchOutput}
	})
	if !res.Blocked {
		t.Fatal("a cargo suite whose real tests were all filtered out must block the commit, not pass")
	}

	logData, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), "vacuous-rejected") {
		t.Fatalf("expected gate.log to carry the vacuous-rejected token, got:\n%s", logData)
	}
}
