package tdd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// nextestTimeoutTranscript is a REAL cargo-nextest 0.9.140 run, captured on
// this box from a two-test crate whose second test never converges under a
// `slow-timeout = { period = "5s", terminate-after = 1 }` profile — the same
// profile shape borld's .config/nextest.toml declares. It is kept verbatim
// (the box-drawing separators included) because the point of every test that
// reads it is what the runner ACTUALLY prints, and a hand-written imitation
// of a runner is evidence about the imitation.
const nextestTimeoutTranscript = "   Compiling forge_powertrain v0.1.0 (C:\\tmp\\fp\\crates\\forge_powertrain)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.33s\n" +
	"────────────\n" +
	" Nextest run ID c74fbfcf-3247-490d-b65f-455f26fb306a with nextest profile: gate\n" +
	"    Starting 2 tests across 2 binaries\n" +
	"        PASS [   0.039s] (1/2) forge_powertrain::locked_diff a_locked_diff_shuts_a_mirrored_gap_without_throwing_it_the_other_way\n" +
	" TERMINATING [>  5.000s] (───) forge_powertrain::locked_diff a_locked_diff_settles_within_the_step_budget\n" +
	"     TIMEOUT [   5.046s] (2/2) forge_powertrain::locked_diff a_locked_diff_settles_within_the_step_budget\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"\n" +
	"    (test timed out)\n" +
	"\n" +
	"  Cancelling due to test failure: \n" +
	"────────────\n" +
	"     Summary [   5.047s] 2 tests run: 1 passed, 1 timed out, 0 skipped\n" +
	"     TIMEOUT [   5.046s] (2/2) forge_powertrain::locked_diff a_locked_diff_settles_within_the_step_budget\n" +
	"error: test run failed\n"

// proveRepo is a committed one-file Go repo a proof can mutate: the mutation
// itself is never what these tests are about, only what the verb does with
// the run it made afterwards.
func proveRepo(t *testing.T) (root, file string) {
	t.Helper()
	root = makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root, filepath.Join(root, "widget.go")
}

// Issue #666's second half, and the worse one: a proof that ruled a red run
// UNREADABLE threw the run's bytes away, so nobody could see the text the
// parse failed on. `aphrollo gate output` serves the last run the gate made
// for a root — every stage that runs a suite retains one, and this verb did
// not, so the record it served was some earlier stage's (clippy, in the
// report) and the mutation run was gone for good. A verdict nobody can audit
// is worse than one that is occasionally wrong: the wrong one at least gets
// noticed.
//
// Asserted on the RETRIEVED bytes, not on a line claiming they were stored.
func TestRunMutantsProve_RetainsTheRunOutputSoGateOutputCanServeIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, file := proveRepo(t)

	var out, errb bytes.Buffer
	RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "a_locked_diff_settles_within_the_step_budget",
	}, fakeRun(false, nextestTimeoutTranscript), &out, &errb)

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput after a proof that ran a suite: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}
	// The run's own bytes — the line the parse had to read, and the summary
	// that says how the runner spelled the failure.
	for _, want := range []string{
		"TIMEOUT [   5.046s] (2/2) forge_powertrain::locked_diff a_locked_diff_settles_within_the_step_budget",
		"2 tests run: 1 passed, 1 timed out, 0 skipped",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("retained record is missing the run's own line %q:\n%s", want, got)
		}
	}
	// And the header that lets a reader tell WHICH run this was: a proof's
	// run is made against a MUTATED tree and must never read as the plain
	// gate run for this root.
	for _, want := range []string{"stage: mutants-prove", "verdict: mutant-"} {
		if !strings.Contains(got, want) {
			t.Errorf("retained record must name %q so a reader knows it is a mutation proof's run:\n%s", want, got)
		}
	}
}

// The other end of the same loop: a stage that reports it could not parse the
// output has to say where the output can be read, or the reader is back to
// the hand rerun the narrowing rule refuses.
func TestRunMutantsProve_UnreadableVerdictNamesTheCommandThatServesTheOutput(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, file := proveRepo(t)

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "TestAdd",
	}, fakeRun(false, "error: could not compile `forge_powertrain` (lib test)\n"), &out, &errb)

	if code != ExitMutantsProveUnreadable {
		t.Fatalf("exit = %d, want ExitMutantsProveUnreadable (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveUnreadable, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "aphrollo gate output") {
		t.Errorf("an unreadable verdict must name the command that serves the text it failed to read: %q", out.String())
	}
}
