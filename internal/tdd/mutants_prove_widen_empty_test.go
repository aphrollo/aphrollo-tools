package tdd

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Issue #758. A proof derives its selection from the mutated file's path: a
// src/ file narrows to `--lib` plus the file's own module filter. When the
// tests that cover the file live in an integration binary under tests/, that
// selection is EMPTY, and the proof refused with NO-TESTS-SELECTED and a hint
// to "widen the scope" that no flag of the command can act on. #691 already
// widens a green narrow run before calling a survivor; an empty one is the
// same situation one step earlier — the narrowing, not the crate, has no
// tests — and gets the same widening before anything is concluded.
//
// Both transcripts are REAL cargo-nextest 0.9.143 runs, captured on this box
// from the crate forgeLabCrate rebuilds, under the mutation `m * v` ->
// `m + v`. Kept line for line; only the scratch directory is shortened.
const libFilterSelectedNothingTranscript = "" +
	"   Compiling forge_lab v0.1.0 (/tmp/fx/crates/forge_lab)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.13s\n" +
	"────────────\n" +
	" Nextest run ID a142b566-f899-46d4-87f5-2e3c381ea2a1 with nextest profile: default\n" +
	"    Starting 0 tests across 1 binary\n" +
	"────────────\n" +
	"     Summary [   0.000s] 0 tests run: 0 passed, 0 skipped\n" +
	"error: no tests to run\n" +
	"(hint: use `--no-tests` to customize)\n"

// The SAME mutated tree with the target narrowing and the filter dropped: the
// integration binary `car` runs, and its test goes red.
const wholeCrateReachesTheIntegrationTestTranscript = "" +
	"   Compiling forge_lab v0.1.0 (/tmp/fx/crates/forge_lab)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.22s\n" +
	"────────────\n" +
	" Nextest run ID 65b4677c-5384-440f-8f2e-ed807bfbb575 with nextest profile: default\n" +
	"    Starting 1 test across 2 binaries\n" +
	"        FAIL [   0.007s] (1/1) forge_lab::car momentum_closure_unit::momentum_closes_over_a_step\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test momentum_closure_unit::momentum_closes_over_a_step ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        momentum_closure_unit::momentum_closes_over_a_step\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'momentum_closure_unit::momentum_closes_over_a_step' (679299) panicked at crates/forge_lab/tests/car/momentum_closure_unit.rs:5:5:\n" +
	"    assertion `left == right` failed\n" +
	"      left: 5.0\n" +
	"     right: 6.0\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"  Cancelling due to test failure: \n" +
	"────────────\n" +
	"     Summary [   0.008s] 1 test run: 0 passed, 1 failed, 0 skipped\n" +
	"        FAIL [   0.007s] (1/1) forge_lab::car momentum_closure_unit::momentum_closes_over_a_step\n" +
	"error: test run failed\n"

// forgeLabCrate is the reproduction's shape: a workspace member whose
// src/car/trace.rs has no unit test at all, covered only by a test in the
// integration binary `car` (tests/car/main.rs mounting its sibling modules).
func forgeLabCrate(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/forge_lab\"]\nresolver = \"2\"\n")
	write(t, root, filepath.FromSlash(".config/nextest.toml"), "[profile.default]\n")
	write(t, root, filepath.FromSlash("crates/forge_lab/Cargo.toml"),
		"[package]\nname = \"forge_lab\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
			"[[test]]\nname = \"car\"\npath = \"tests/car/main.rs\"\n")
	write(t, root, filepath.FromSlash("crates/forge_lab/src/lib.rs"), "pub mod car;\n")
	write(t, root, filepath.FromSlash("crates/forge_lab/src/car.rs"), "pub mod trace;\n")
	write(t, root, filepath.FromSlash("crates/forge_lab/src/car/trace.rs"),
		"pub fn momentum(m: f64, v: f64) -> f64 {\n    m * v\n}\n")
	write(t, root, filepath.FromSlash("crates/forge_lab/tests/car/main.rs"), "mod momentum_closure_unit;\n")
	write(t, root, filepath.FromSlash("crates/forge_lab/tests/car/momentum_closure_unit.rs"),
		"use forge_lab::car::trace::momentum;\n\n#[test]\nfn momentum_closes_over_a_step() {\n"+
			"    assert_eq!(momentum(2.0, 3.0), 6.0);\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root, filepath.Join(root, "crates", "forge_lab", "src", "car", "trace.rs")
}

func TestRunMutantsProve_WidensAnEmptyLibSelectionToTheCratesIntegrationTests(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, file := forgeLabCrate(t)

	var ran []Runner
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "m * v",
		New:      "m + v",
		WantFail: "momentum_closes_over_a_step",
	}, func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		if slices.Contains(r.Args, "--lib") {
			// nextest exits 4 on an empty selection: a non-passing run.
			return SuiteResult{Passed: false, Output: libFilterSelectedNothingTranscript}
		}
		return SuiteResult{Passed: false, Output: wholeCrateReachesTheIntegrationTestTranscript}
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s\nselections run: %s",
			code, ExitMutantsProveKilled, report, cmdStrings(ran))
	}
	if !strings.Contains(report, "momentum_closure_unit::momentum_closes_over_a_step") {
		t.Errorf("the verdict never names the integration test that killed the mutant:\n%s", report)
	}
	if len(ran) != 2 {
		t.Fatalf("selections run = %s, want the narrow one then the widened one", cmdStrings(ran))
	}
	if slices.Contains(ran[1].Args, "--lib") || slices.Contains(ran[1].Args, "-E") {
		t.Errorf("the widened run still narrows within the crate: %s", cmdString(ran[1]))
	}
	if !slices.Contains(ran[1].Args, "forge_lab") {
		t.Errorf("widening dropped the package scope: %s", cmdString(ran[1]))
	}
}

// The Go arm of the same rule. A Go source file narrows to its own package,
// and a package with no test files runs NOTHING: `go test` prints
// `? <pkg> [no test files]` and exits 0. With no importer to widen to, that
// empty green was reported as a SURVIVOR — a claim that the tests do not
// constrain the line, made by a run that executed no test. Runs the real
// `go test` and the real `go list` behind the widening.
func TestRunMutantsProve_AGoPackageWithNoTestsAnywhereIsNotASurvivor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("torque/torque.go"),
		"package torque\n\n// Split hands the input torque to both half-shafts.\n"+
			"func Split(input float64) (float64, float64) {\n\thalf := input / 2.0\n\treturn half, half\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "untested package")

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "torque", "torque.go"),
		Old:      "input / 2.0",
		New:      "input / 4.0",
		WantFail: "TestSplitConservesTorque",
	}, RunSuite(precommitTestTimeout), &out, &errb)
	report := out.String() + errb.String()

	if code == ExitMutantsProveSurvived {
		t.Fatalf("a run that executed no test was reported as a SURVIVOR:\n%s", report)
	}
	if code != ExitMutantsProveNoTestsSelected {
		t.Fatalf("exit = %d, want ExitMutantsProveNoTestsSelected (%d):\n%s",
			code, ExitMutantsProveNoTestsSelected, report)
	}
	// The refusal's advice has to fit the runner that produced it: a Go
	// package has no `#[path]` mount to check.
	if strings.Contains(report, "#[path") {
		t.Errorf("a Go refusal gave Rust's module-path advice:\n%s", report)
	}
}

// When the widened run is empty too, the refusal must say the widening
// already happened and from what: a reader told to "check the filter" goes
// looking at a narrow filter the proof has already stepped past.
func TestRunMutantsProve_AnEmptySelectionEvenAfterWideningNamesBothSelections(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, file := forgeLabCrate(t)

	var ran []Runner
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "m * v",
		New:      "m + v",
		WantFail: "momentum_closes_over_a_step",
	}, func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		return SuiteResult{Passed: false, Output: libFilterSelectedNothingTranscript}
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveNoTestsSelected {
		t.Fatalf("exit = %d, want ExitMutantsProveNoTestsSelected (%d):\n%s\nselections run: %s",
			code, ExitMutantsProveNoTestsSelected, report, cmdStrings(ran))
	}
	if len(ran) != 2 {
		t.Fatalf("selections run = %s, want the narrow one then the widened one", cmdStrings(ran))
	}
	if !strings.Contains(report, cmdString(ran[0])) || !strings.Contains(report, cmdString(ran[1])) {
		t.Errorf("the refusal does not name both the narrow selection (%s) and the widened one (%s):\n%s",
			cmdString(ran[0]), cmdString(ran[1]), report)
	}
}

// goRanNoTests reads `go test`'s own per-package lines. Each shape below is
// one `go test` prints; the CRLF case is the same output on Windows. Only a
// run in which EVERY package ran nothing is empty: one package that ran a
// test is evidence, and a run with no package line at all is a failure.
func TestGoRanNoTests_ReadsOnlyARunInWhichEveryPackageRanNothing(t *testing.T) {
	goRun := Runner{Cmd: "go", Args: []string{"test", "./torque"}}
	cases := []struct {
		name   string
		runner Runner
		res    SuiteResult
		want   bool
	}{
		{"a package with no test files", goRun,
			SuiteResult{Passed: true, Output: "?   \texample.com/m/torque\t[no test files]\n"}, true},
		{"a -run filter that matched nothing", goRun,
			SuiteResult{Passed: true, Output: "ok  \texample.com/m/torque\t0.004s [no tests to run]\n"}, true},
		{"the same on Windows", goRun,
			SuiteResult{Passed: true, Output: "ok  \texample.com/m/torque\t0.004s [no tests to run]\r\n"}, true},
		{"a package whose tests ran", goRun,
			SuiteResult{Passed: true, Output: "ok  \texample.com/m/torque\t0.003s\n"}, false},
		{"one empty package beside one that ran tests", goRun,
			SuiteResult{Passed: true, Output: "?   \texample.com/m/torque\t[no test files]\n" +
				"ok  \texample.com/m/driveline\t0.003s\n"}, false},
		{"a failing package", goRun,
			SuiteResult{Output: "?   \texample.com/m/torque\t[no test files]\n" +
				"FAIL\texample.com/m/driveline\t0.003s\n"}, false},
		{"no package line at all", goRun,
			SuiteResult{Output: "go: cannot find main module\n"}, false},
		{"a timed-out run", goRun,
			SuiteResult{TimedOut: true, Output: "?   \texample.com/m/torque\t[no test files]\n"}, false},
		{"a cargo runner", Runner{Cmd: "cargo", Args: []string{"test"}},
			SuiteResult{Passed: true, Output: "?   \texample.com/m/torque\t[no test files]\n"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := goRanNoTests(c.runner, c.res); got != c.want {
				t.Fatalf("goRanNoTests = %v, want %v for output %q", got, c.want, c.res.Output)
			}
		})
	}
}
