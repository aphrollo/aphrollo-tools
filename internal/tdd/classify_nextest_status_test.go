package tdd

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// Issue #666's first half: `gate mutants prove` ruled a mutation UNREADABLE —
// "no failing test name could be read from the output" — over a run in which
// cargo-nextest printed the failing test BY NAME, twice. A verdict that
// contradicts its own evidence.
//
// The cause is not the --want-fail spelling (matchWantFail accepts a suffix
// and then a substring, and UNREADABLE is only reached when the failing set
// is EMPTY, where the wanted name plays no part at all). It is that the
// extractor knows exactly ONE nextest failure spelling, `FAIL [ … ]`, while a
// test can fail under several — and which one a repo sees is decided by its
// nextest profile, not by the test:
//
//   - a slow-timeout with `terminate-after` prints TIMEOUT (borld's
//     .config/nextest.toml sets one on every profile);
//   - a process that dies rather than failing an assertion prints ABORT
//     (SIGSEGV/SIGABRT/… on unix);
//   - and with `retries` set, a failing test is NEVER spelled bare `FAIL` —
//     every line, the final summary's included, reads `TRY <n> FAIL`.
//
// The three transcripts below are REAL cargo-nextest 0.9.140 runs, captured
// on this box from a two-test crate under the profiles named above, kept line
// for line (only the scratch directory is shortened) because a hand-written
// imitation of a runner is evidence about the imitation.

const nextestAbortTranscript = "   Compiling forge_powertrain v0.1.0 (C:\\tmp\\fp\\crates\\forge_powertrain)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.25s\n" +
	"────────────\n" +
	" Nextest run ID b308a3b0-54d7-45ec-9348-7974b7661340 with nextest profile: default\n" +
	"    Starting 2 tests across 1 binary\n" +
	"        FAIL [   0.012s] (1/2) forge_powertrain::shapes a_plain_assert_goes_red\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test a_plain_assert_goes_red ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        a_plain_assert_goes_red\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 1 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'a_plain_assert_goes_red' (15396) panicked at crates\\forge_powertrain\\tests\\shapes.rs:8:5:\n" +
	"    assertion `left == right` failed: arithmetic moved\n" +
	"      left: 2\n" +
	"     right: 3\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"       ABORT [   0.090s] (2/2) forge_powertrain::shapes a_hard_abort_takes_the_process_down\n" +
	"           - with code 0xc0000409: The system detected an overrun of a stack-based buffer in this application. This overrun could potentially allow a malicious user to gain control of this application. (os error 1282)\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"\n" +
	"    (test aborted)\n" +
	"\n" +
	"────────────\n" +
	"     Summary [   0.092s] 2 tests run: 0 passed, 2 failed, 0 skipped\n" +
	"        FAIL [   0.012s] (1/2) forge_powertrain::shapes a_plain_assert_goes_red\n" +
	"       ABORT [   0.090s] (2/2) forge_powertrain::shapes a_hard_abort_takes_the_process_down\n" +
	"           - with code 0xc0000409: The system detected an overrun of a stack-based buffer in this application. This overrun could potentially allow a malicious user to gain control of this application. (os error 1282)\n" +
	"error: test run failed\n"

const nextestRetryTranscript = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.00s\n" +
	"────────────\n" +
	" Nextest run ID 9c6f8e59-5aa5-4d4d-bbc9-6da49f5d5b52 with nextest profile: retry\n" +
	"    Starting 1 test across 1 binary (1 test skipped)\n" +
	"  TRY 1 FAIL [   0.007s] (───) forge_powertrain::shapes a_plain_assert_goes_red\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test a_plain_assert_goes_red ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        a_plain_assert_goes_red\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 1 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'a_plain_assert_goes_red' (6080) panicked at crates\\forge_powertrain\\tests\\shapes.rs:8:5:\n" +
	"    assertion `left == right` failed: arithmetic moved\n" +
	"      left: 2\n" +
	"     right: 3\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"  TRY 2 FAIL [   0.005s] (───) forge_powertrain::shapes a_plain_assert_goes_red\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test a_plain_assert_goes_red ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        a_plain_assert_goes_red\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 1 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'a_plain_assert_goes_red' (944) panicked at crates\\forge_powertrain\\tests\\shapes.rs:8:5:\n" +
	"    assertion `left == right` failed: arithmetic moved\n" +
	"      left: 2\n" +
	"     right: 3\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"  TRY 3 FAIL [   0.007s] (1/1) forge_powertrain::shapes a_plain_assert_goes_red\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test a_plain_assert_goes_red ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        a_plain_assert_goes_red\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 1 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'a_plain_assert_goes_red' (18512) panicked at crates\\forge_powertrain\\tests\\shapes.rs:8:5:\n" +
	"    assertion `left == right` failed: arithmetic moved\n" +
	"      left: 2\n" +
	"     right: 3\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"────────────\n" +
	"     Summary [   0.037s] 1 test run: 0 passed, 1 failed, 1 skipped\n" +
	"  TRY 3 FAIL [   0.007s] (1/1) forge_powertrain::shapes a_plain_assert_goes_red\n" +
	"error: test run failed\n"

// Each transcript is a red run (nextest exits 100) that names its failing
// test. Whatever status the runner chose to spell it with, the name is
// readable, and a stage that reports the output as unreadable over one of
// these is wrong about its own evidence.
func TestExtractFailingTests_ReadsTheNameNextestPrintsUnderEveryFailingStatus(t *testing.T) {
	for _, c := range []struct {
		what   string
		output string
		want   string
	}{
		{"a test the runner terminated on its slow-timeout", nextestTimeoutTranscript, "a_locked_diff_settles_within_the_step_budget"},
		{"a test whose process died instead of failing an assertion", nextestAbortTranscript, "a_hard_abort_takes_the_process_down"},
		{"a test that failed every retry", nextestRetryTranscript, "a_plain_assert_goes_red"},
	} {
		if got := ExtractFailingTests(c.output); !slices.Contains(got, c.want) {
			t.Errorf("%s: ExtractFailingTests = %v, missing the name the run printed (%q)", c.what, got, c.want)
		}
	}
}

// The other direction, and the one that makes the statuses above safe to add:
// a status that is NOT a failure must never be read as one. The timeout
// transcript carries a PASS line for the crate's other test and a TERMINATING
// progress line for the one that later timed out — a WRONG FAILURE verdict
// naming a test that passed would be the same class of defect as #666 itself,
// pointed the other way.
func TestExtractFailingTests_NeverReadsANextestPassAsAFailure(t *testing.T) {
	got := ExtractFailingTests(nextestTimeoutTranscript)
	if slices.Contains(got, "a_locked_diff_shuts_a_mirrored_gap_without_throwing_it_the_other_way") {
		t.Errorf("ExtractFailingTests = %v, reads the PASS line's test as failing", got)
	}
}

// The end-to-end shape issue #666 reported, on the transcript that produced
// it: the mutation is applied, the runner reddens and names the test the
// prediction called, so the proof is KILLED — never UNREADABLE.
func TestRunMutantsProve_KilledWhenNextestReddensTheNamedTestByTimeout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, file := proveRepo(t)

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "a_locked_diff_settles_within_the_step_budget",
	}, fakeRun(false, nextestTimeoutTranscript), &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveKilled, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "a_locked_diff_settles_within_the_step_budget") {
		t.Errorf("the KILLED report must name the test that went red: %q", out.String())
	}
}
