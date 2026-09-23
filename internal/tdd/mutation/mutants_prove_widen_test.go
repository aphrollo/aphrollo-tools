package mutation

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Issue #691. A mutation proof reported SURVIVOR for a mutant the crate's
// tests DO kill, because the selection it ran could not have reached the
// killing test: a src/ edit is narrowed to `--lib` plus the file's module
// filter, and `--lib` never builds or runs the integration binary under
// tests/. Nothing in the output said so.
//
// That is worse than an UNREADABLE verdict, which announces its own
// uncertainty. A false SURVIVOR is a confident wrong answer in the direction
// that BLOCKS correct work — the pre-merge gate refuses an unaccepted
// survivor by name — over evidence that does not exist.
//
// The two transcripts below are REAL cargo-nextest 0.9.140 runs, captured on
// this box from the crate the fixture below rebuilds: a `split_torque` whose
// only unit test asserts the pair is finite (true under the mutation) and
// whose integration test in tests/locked_diff.rs asserts the split conserves
// the input torque (false under it). Kept line for line, because the whole
// question here is which tests the runner ACTUALLY selects, and a
// hand-written imitation of a runner is evidence about the imitation.
//
// The mutation both runs were made under is `input / 2.0` -> `input / 4.0`.
const narrowedToLibTranscript = "   Compiling forge_driveline v0.1.0 (C:\\tmp\\fx\\crates\\forge_driveline)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.18s\n" +
	"────────────\n" +
	" Nextest run ID 1724fbe6-ab0a-49f2-befa-9444c69cb02b with nextest profile: default\n" +
	"    Starting 1 test across 1 binary\n" +
	"        PASS [   0.007s] (1/1) forge_driveline final_drive::tests::split_torque_returns_a_finite_pair\n" +
	"────────────\n" +
	"     Summary [   0.008s] 1 test run: 1 passed, 0 skipped\n"

// The SAME mutated tree, measured by the same runner with the target
// narrowing dropped: two binaries instead of one, and the integration test
// the `--lib` run never saw goes red.
const widenedToAllTargetsTranscript = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.27s\n" +
	"────────────\n" +
	" Nextest run ID 166e457e-f297-4663-a0e3-aad098c55307 with nextest profile: default\n" +
	"    Starting 2 tests across 2 binaries\n" +
	"        PASS [   0.008s] (1/2) forge_driveline final_drive::tests::split_torque_returns_a_finite_pair\n" +
	"        FAIL [   0.012s] (2/2) forge_driveline::locked_diff a_locked_diff_conserves_the_input_torque\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test a_locked_diff_conserves_the_input_torque ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        a_locked_diff_conserves_the_input_torque\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'a_locked_diff_conserves_the_input_torque' (27808) panicked at crates\\forge_driveline\\tests\\locked_diff.rs:6:5:\n" +
	"    assertion `left == right` failed: a locked differential conserves torque\n" +
	"      left: 200.0\n" +
	"     right: 400.0\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"  Cancelling due to test failure: \n" +
	"────────────\n" +
	"     Summary [   0.012s] 2 tests run: 1 passed, 1 failed, 0 skipped\n" +
	"        FAIL [   0.012s] (2/2) forge_driveline::locked_diff a_locked_diff_conserves_the_input_torque\n" +
	"error: test run failed\n"

// killingTestIsAnIntegrationTest is the name of the test in tests/ that the
// `--lib` selection cannot reach, and the one a proof must be judged on.
const killingTestIsAnIntegrationTest = "a_locked_diff_conserves_the_input_torque"

// driveShaftCrate is a committed single-crate cargo repo shaped exactly like
// the reproduction: the mutated function lives in src/, its one unit test
// does not constrain the mutated line, and the test that does lives in an
// integration binary under tests/. Only that layout can produce the defect —
// a unit-test fixture passes against the broken code.
func driveShaftCrate(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_driveline\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, filepath.FromSlash(".config/nextest.toml"), "[profile.default]\n")
	write(t, root, filepath.FromSlash("src/lib.rs"), "pub mod final_drive;\n")
	write(t, root, filepath.FromSlash("src/final_drive.rs"),
		"pub fn split_torque(input: f64) -> (f64, f64) {\n    let half = input / 2.0;\n    (half, half)\n}\n")
	write(t, root, filepath.FromSlash("tests/locked_diff.rs"),
		"use forge_driveline::final_drive::split_torque;\n\n#[test]\nfn "+killingTestIsAnIntegrationTest+"() {\n"+
			"    let (left, right) = split_torque(400.0);\n    assert_eq!(left + right, 400.0);\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root, filepath.Join(root, "src", "final_drive.rs")
}

// byTargetSelection replays the transcript the REAL runner produced for the
// selection it is handed: the `--lib` run that never reaches tests/, and the
// same tree with that narrowing dropped. It records every runner it was
// called with, in order, so a caller can assert WHICH run the verdict came
// from.
func byTargetSelection(ran *[]Runner) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		*ran = append(*ran, r)
		if slices.Contains(r.Args, "--lib") {
			return SuiteResult{Passed: true, Output: narrowedToLibTranscript}
		}
		return SuiteResult{Passed: false, Output: widenedToAllTargetsTranscript}
	}
}

// The defect itself: the proof must not answer SURVIVOR out of a selection
// that excluded the only test able to kill the mutant. It has to widen and
// let the wider run answer — a survivor is the rare branch, so the cost of
// building and running the integration binaries lands only where the narrow
// answer would otherwise have been wrong.
func TestRunMutantsProve_NeverCallsASurvivorOnASelectionThatCannotReachTheKillingTest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, file := driveShaftCrate(t)

	var ran []Runner
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "input / 2.0",
		New:      "input / 4.0",
		WantFail: killingTestIsAnIntegrationTest,
	}, byTargetSelection(&ran), &out, &errb)

	report := out.String() + errb.String()
	if code == ExitMutantsProveSurvived {
		t.Fatalf("a mutant the crate's integration test DOES kill was reported as a survivor:\n%s\nselections run: %s",
			report, cmdStrings(ran))
	}
	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s\nselections run: %s",
			code, ExitMutantsProveKilled, report, cmdStrings(ran))
	}
	if !strings.Contains(report, killingTestIsAnIntegrationTest) {
		t.Fatalf("the verdict never names the integration test that killed the mutant:\n%s", report)
	}
}

// The trap of a two-phase verdict: the narrow phase ran first and came back
// green, so if anything downstream of the widening — the printed verdict, the
// retained run `aphrollo gate output` serves — still describes the NARROW
// run, the proof is back to reporting evidence that could not have reached
// the killing test. The record has to be the widened run's.
func TestRunMutantsProve_RecordsTheWidenedRunNotTheNarrowOneItStartedWith(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, file := driveShaftCrate(t)

	var ran []Runner
	var out, errb bytes.Buffer
	RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "input / 2.0",
		New:      "input / 4.0",
		WantFail: killingTestIsAnIntegrationTest,
	}, byTargetSelection(&ran), &out, &errb)

	if len(ran) != 2 {
		t.Fatalf("selections run = %s, want the narrow one then the widened one", cmdStrings(ran))
	}
	if !slices.Contains(ran[0].Args, "--lib") {
		t.Errorf("the first run must be the cheap narrowed one: %s", cmdString(ran[0]))
	}
	if slices.Contains(ran[1].Args, "--lib") {
		t.Errorf("the widened run still restricts the targets to the lib: %s", cmdString(ran[1]))
	}
	if !slices.Contains(ran[1].Args, "-p") || !slices.Contains(ran[1].Args, "forge_driveline") {
		t.Errorf("widening dropped the package scope as well as the target narrowing: %s", cmdString(ran[1]))
	}

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput after a widened proof: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}
	if !strings.Contains(got, "2 tests run: 1 passed, 1 failed, 0 skipped") {
		t.Errorf("the retained run is not the widened one — its bytes must be the evidence the verdict was read from:\n%s", got)
	}
	if strings.Contains(got, "1 test run: 1 passed, 0 skipped") {
		t.Errorf("the narrow run's bytes were retained as the proof's record:\n%s", got)
	}
	// And the header has to name the command those bytes came from: a record
	// headed by the `--lib` selection over the widened run's output tells a
	// reader auditing the verdict that the narrow run found the failure.
	// Matched as a whole line: the widened command is a PREFIX of the narrow
	// one (widening only drops flags), so a substring test would read the
	// narrow header as the widened one and pass over the exact confusion it
	// is here to catch.
	if !strings.Contains(got, "command: "+cmdString(ran[1])+"\n") {
		t.Errorf("the retained record does not name the widened command %q it was produced by:\n%s",
			cmdString(ran[1]), got)
	}
}

// The two arms have to agree on what a verdict MEANS, and this is the case
// where they did not. The Go arm reports SCOPE UNKNOWN when it cannot widen
// into a selection able to kill the mutant; the cargo arm had one such case
// of its own and reported a SURVIVOR for it.
//
// An `examples/` (or `benches/`) file narrows to `--example <name> --no-run`:
// a COMPILE check that runs no test at all (buildonly.go), and one
// widenCargoRunner refuses to widen into the package's whole suite. Green
// there means "it still builds", which is no evidence about what the tests
// constrain — the same nothing the Go arm calls inconclusive, and the reading
// "survivor" is the one most likely to be believed and acted on.
func TestRunMutantsProve_ABuildOnlySelectionIsInconclusiveNotASurvivor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_driveline\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, filepath.FromSlash("src/lib.rs"), "pub fn base() -> i32 {\n    0\n}\n")
	write(t, root, filepath.FromSlash("examples/nubis/capture.rs"),
		"fn main() {\n    let half = 400.0 / 2.0;\n    println!(\"{half}\");\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	var ran []Runner
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, "examples", "nubis", "capture.rs"),
		Old:  "400.0 / 2.0",
		New:  "400.0 / 4.0",
		// The build-only run cannot fail this or any other test: it runs none.
		WantFail: killingTestIsAnIntegrationTest,
	}, func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		// What a `--no-run` build prints: it compiled, and no test ran.
		return SuiteResult{Passed: true, Output: "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.21s\n"}
	}, &out, &errb)

	report := out.String() + errb.String()
	if code == ExitMutantsProveSurvived {
		t.Fatalf("a build-only run — no test executed at all — was reported as a survivor:\n%s\nselections run: %s",
			report, cmdStrings(ran))
	}
	if code != ExitMutantsProveScopeUnknown {
		t.Fatalf("exit = %d, want ExitMutantsProveScopeUnknown (%d), the same verdict the Go arm gives a "+
			"selection it cannot widen:\n%s\nselections run: %s",
			code, ExitMutantsProveScopeUnknown, report, cmdStrings(ran))
	}
	if !strings.Contains(strings.ToLower(report), "inconclusive") {
		t.Errorf("the verdict never says it is inconclusive:\n%s", report)
	}
	if len(ran) != 1 {
		t.Errorf("a build-only selection must not be widened into the package's whole suite: %s", cmdStrings(ran))
	}
}

// cmdStrings renders the selections a proof actually ran, for a failure
// message that shows WHICH commands produced the verdict under judgement.
func cmdStrings(ran []Runner) string {
	out := make([]string, 0, len(ran))
	for _, r := range ran {
		out = append(out, cmdString(r))
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, " | ")
}
