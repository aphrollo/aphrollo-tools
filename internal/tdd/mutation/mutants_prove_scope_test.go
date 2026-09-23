package mutation

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A proof names the test its mutation must fail (--want-fail), and ran a
// selection that ignored that name: the mutated file's module under `--lib`,
// with nextest's fail-fast on. A mutation that a FASTER unit test in the same
// module also catches then fails that test first, fail-fast cancels the rest,
// and the wanted integration test never runs: the proof reported WRONG
// FAILURE, or UNREADABLE, for a mutant the named test does kill (#744's
// second report). The run is now scoped to the named test itself, with
// fail-fast off, and widens only when that selection is empty.
//
// The four transcripts are REAL cargo-nextest 0.9.143 runs, captured on this
// box from the crate solverCrate rebuilds (test-threads = 1, so the order is
// the order nextest printed), under the mutation `lambda * alpha * gamma` ->
// `lambda * alpha * gamma * 4.0`. Kept line for line; only the scratch
// directory is shortened.

// `cargo nextest run -p forge_solver --lib -E test(/^dual::/)`: the module
// selection the proof used to run.
const moduleSelectionHitsTheFastUnitTestTranscript = "" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.04s\n" +
	"────────────\n" +
	" Nextest run ID 8c5687f9-c999-41c6-9d18-84770f2da965 with nextest profile: default\n" +
	"    Starting 1 test across 1 binary\n" +
	"        FAIL [   0.007s] (1/1) forge_solver dual::tests::warmstart_decays_lambda\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test dual::tests::warmstart_decays_lambda ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        dual::tests::warmstart_decays_lambda\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'dual::tests::warmstart_decays_lambda' (3986493) panicked at src/dual/mod.rs:11:9:\n" +
	"    assertion failed: decay(1.0, 0.5, 0.5) < 1.0\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"  Cancelling due to test failure: \n" +
	"────────────\n" +
	"     Summary [   0.009s] 1 test run: 0 passed, 1 failed, 0 skipped\n" +
	"        FAIL [   0.007s] (1/1) forge_solver dual::tests::warmstart_decays_lambda\n" +
	"error: test run failed\n"

// `cargo nextest run -p forge_solver`: the whole package, fail-fast on. The
// unit test fails first and the integration binary is cancelled.
const wholePackageFailFastTranscript = "" +
	"   Compiling forge_solver v0.1.0 (/tmp/fx)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.33s\n" +
	"────────────\n" +
	" Nextest run ID 1a1aca47-052e-4184-a7f7-d4e8913de3c4 with nextest profile: default\n" +
	"    Starting 2 tests across 2 binaries\n" +
	"        FAIL [   0.010s] (1/2) forge_solver dual::tests::warmstart_decays_lambda\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test dual::tests::warmstart_decays_lambda ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        dual::tests::warmstart_decays_lambda\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'dual::tests::warmstart_decays_lambda' (3986136) panicked at src/dual/mod.rs:11:9:\n" +
	"    assertion failed: decay(1.0, 0.5, 0.5) < 1.0\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"  Cancelling due to test failure: \n" +
	"────────────\n" +
	"     Summary [   0.011s] 1/2 tests run: 0 passed, 1 failed, 0 skipped\n" +
	"        FAIL [   0.010s] (1/2) forge_solver dual::tests::warmstart_decays_lambda\n" +
	"warning: 1/2 tests were not run due to test failure (run with --no-fail-fast to run all tests, or run with --max-fail)\n" +
	"error: test run failed\n"

// The lib target scoped to the wanted test: it is not in the lib.
const libScopedToTheWantedTestTranscript = "" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.02s\n" +
	"────────────\n" +
	" Nextest run ID 7aabc08b-cc0e-44a2-b2cc-180056a7754e with nextest profile: default\n" +
	"    Starting 0 tests across 1 binary (1 test skipped)\n" +
	"────────────\n" +
	"     Summary [   0.000s] 0 tests run: 0 passed, 1 skipped\n" +
	"error: no tests to run\n" +
	"(hint: use `--no-tests` to customize)\n"

// The package scoped to the wanted test, fail-fast off.
const packageScopedToTheWantedTestTranscript = "" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.01s\n" +
	"────────────\n" +
	" Nextest run ID ae02fcd0-66c1-452e-b609-4223784b364a with nextest profile: default\n" +
	"    Starting 1 test across 2 binaries (1 test skipped)\n" +
	"        FAIL [   0.007s] (1/1) forge_solver::integration the_hard_rows_settled_stretch\n" +
	"  stdout ───\n" +
	"\n" +
	"    running 1 test\n" +
	"    test the_hard_rows_settled_stretch ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        the_hard_rows_settled_stretch\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
	"\n" +
	"  stderr ───\n" +
	"\n" +
	"    thread 'the_hard_rows_settled_stretch' (3986360) panicked at tests/integration.rs:5:5:\n" +
	"    assertion `left == right` failed\n" +
	"      left: 8.0\n" +
	"     right: 2.0\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n" +
	"\n" +
	"────────────\n" +
	"     Summary [   0.009s] 1 test run: 0 passed, 1 failed, 1 skipped\n" +
	"        FAIL [   0.007s] (1/1) forge_solver::integration the_hard_rows_settled_stretch\n" +
	"error: test run failed\n"

const wantedIntegrationTest = "the_hard_rows_settled_stretch"

// solverCrate is the reported shape: src/dual/mod.rs carries its own unit
// test, and the test the proof is about lives in the integration binary.
func solverCrate(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_solver\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, filepath.FromSlash(".config/nextest.toml"), "[profile.default]\ntest-threads = 1\n")
	write(t, root, filepath.FromSlash("src/lib.rs"), "pub mod dual;\n")
	write(t, root, filepath.FromSlash("src/dual/mod.rs"),
		"pub fn decay(lambda: f64, alpha: f64, gamma: f64) -> f64 {\n    lambda * alpha * gamma\n}\n\n"+
			"#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn warmstart_decays_lambda() {\n"+
			"        assert!(decay(1.0, 0.5, 0.5) < 1.0);\n    }\n}\n")
	write(t, root, filepath.FromSlash("tests/integration.rs"),
		"use forge_solver::dual::decay;\n\n#[test]\nfn "+wantedIntegrationTest+"() {\n"+
			"    assert_eq!(decay(8.0, 0.5, 0.5), 2.0);\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root, filepath.Join(root, "src", "dual", "mod.rs")
}

// namesTheWantedTest reports whether a cargo runner's filter selects the test
// by name, in either dialect.
func namesTheWantedTest(r Runner, want string) bool {
	return slices.ContainsFunc(r.Args, func(a string) bool { return strings.Contains(a, want) })
}

func TestRunMutantsProve_AFasterUnrelatedTestCannotPreemptTheWantedOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, file := solverCrate(t)

	var ran []Runner
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "lambda * alpha * gamma",
		New:      "lambda * alpha * gamma * 4.0",
		WantFail: wantedIntegrationTest,
	}, func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		lib := slices.Contains(r.Args, "--lib")
		switch {
		case namesTheWantedTest(r, wantedIntegrationTest) && lib:
			return SuiteResult{Output: libScopedToTheWantedTestTranscript}
		case namesTheWantedTest(r, wantedIntegrationTest):
			return SuiteResult{Output: packageScopedToTheWantedTestTranscript}
		case lib:
			return SuiteResult{Output: moduleSelectionHitsTheFastUnitTestTranscript}
		}
		return SuiteResult{Output: wholePackageFailFastTranscript}
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s\nselections run: %s",
			code, ExitMutantsProveKilled, report, cmdStrings(ran))
	}
	if !strings.Contains(report, wantedIntegrationTest+" failed as predicted") {
		t.Errorf("the verdict is not the wanted test's kill:\n%s", report)
	}
	for _, r := range ran {
		if !slices.Contains(r.Args, "--no-fail-fast") {
			t.Errorf("a proof run kept fail-fast on, so a faster test can still cancel the wanted one: %s", cmdString(r))
		}
	}
}

// The Go arm: the run is scoped to the named test with `-run`, so another
// test in the package, failing under the same mutation, is not run at all.
// Real `go test`.
func TestRunMutantsProve_AGoProofRunsOnlyTheNamedTest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("torque/torque.go"),
		"package torque\n\nfunc Split(input float64) float64 {\n\treturn input / 2.0\n}\n")
	write(t, root, filepath.FromSlash("torque/torque_test.go"),
		"package torque\n\nimport \"testing\"\n\n"+
			"func TestSplitHalves(t *testing.T) {\n\tif Split(4) != 2 {\n\t\tt.Fatal(\"not half\")\n\t}\n}\n\n"+
			"func TestSplitOfEight(t *testing.T) {\n\tif Split(8) != 4 {\n\t\tt.Fatal(\"not half of eight\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "torque")

	var outputs []string
	real := RunSuite(precommitTestTimeout)
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "torque", "torque.go"),
		Old:      "input / 2.0",
		New:      "input / 4.0",
		WantFail: "TestSplitHalves",
	}, func(r Runner, dir string) SuiteResult {
		res := real(r, dir)
		outputs = append(outputs, res.Output)
		return res
	}, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s", code, ExitMutantsProveKilled, report)
	}
	if len(outputs) != 1 {
		t.Fatalf("runs = %d, want the one scoped run:\n%s", len(outputs), report)
	}
	if strings.Contains(outputs[0], "TestSplitOfEight") {
		t.Errorf("the proof's run executed a test it was not asked about:\n%s", outputs[0])
	}
}

// A --want-fail that is a unique SUBSTRING of the test's name (the flag
// allows it) selects nothing under an exact `-run`; the proof then widens,
// dropping the name filter, rather than refusing. Real `go test`.
func TestRunMutantsProve_AGoWantFailThatMatchesNoExactNameWidens(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("torque/torque.go"),
		"package torque\n\nfunc Split(input float64) float64 {\n\treturn input / 2.0\n}\n")
	write(t, root, filepath.FromSlash("torque/torque_test.go"),
		"package torque\n\nimport \"testing\"\n\n"+
			"func TestSplitHalves(t *testing.T) {\n\tif Split(4) != 2 {\n\t\tt.Fatal(\"not half\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "torque")

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "torque", "torque.go"),
		Old:      "input / 2.0",
		New:      "input / 4.0",
		WantFail: "SplitHalves",
	}, RunSuite(precommitTestTimeout), &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s", code, ExitMutantsProveKilled, report)
	}
}

// Each runner dialect gets the wanted test's filter in its own syntax, with
// fail-fast off for cargo; a name no filter can carry verbatim, and a
// build-only runner, keep the selection they had.
func TestScopeToWantedTest_SpeaksEachRunnersFilterDialect(t *testing.T) {
	cases := []struct {
		name string
		in   Runner
		want string
		out  []string
	}{
		{"nextest module filter", Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_lab", "--lib", "-E", "test(/^car::trace::/)"}},
			"momentum_closes_over_a_step",
			[]string{"nextest", "run", "-p", "forge_lab", "--lib", "--no-fail-fast", "-E", "test(/(^|::)momentum_closes_over_a_step$/)"}},
		{"cargo test module filter", Runner{Cmd: "cargo", Args: []string{"test", "-p", "forge_lab", "--lib", "car::trace::"}},
			"momentum_closes_over_a_step",
			[]string{"test", "-p", "forge_lab", "--lib", "--no-fail-fast", "momentum_closes_over_a_step"}},
		{"a module-qualified want", Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "m", "--lib"}},
			"dual::tests::warmstart",
			[]string{"nextest", "run", "-p", "m", "--lib", "--no-fail-fast", "-E", "test(/(^|::)dual::tests::warmstart$/)"}},
		{"a want no filter can carry", Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "m", "--lib"}},
			"a test (with parens)",
			[]string{"nextest", "run", "-p", "m", "--lib", "--no-fail-fast"}},
		{"a build-only example", Runner{Cmd: "cargo", Args: []string{"test", "--example", "nubis", "--no-run"}},
			"anything",
			[]string{"test", "--example", "nubis", "--no-run"}},
		{"go", Runner{Cmd: "go", Args: []string{"test", "./torque"}},
			"TestSplitHalves",
			[]string{"test", "-run=^TestSplitHalves$", "./torque"}},
		{"a go subtest path", Runner{Cmd: "go", Args: []string{"test", "./torque"}},
			"TestSplit/halves",
			[]string{"test", "./torque"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scopeToWantedTest(c.in, c.want)
			if !slices.Equal(got.Args, c.out) || got.Cmd != c.in.Cmd {
				t.Fatalf("scopeToWantedTest(%s, %q) = %s, want %s %s",
					cmdString(c.in), c.want, cmdString(got), c.in.Cmd, strings.Join(c.out, " "))
			}
		})
	}
}

// dropCargoTarget is the rung between a scoped target and the whole package:
// it removes the target in every spelling and nothing else.
func TestDropCargoTarget_RemovesOnlyTheTargetSelection(t *testing.T) {
	filter := []string{"-E", "test(/(^|::)x$/)"}
	cases := []struct {
		name string
		args []string
		out  []string
		ok   bool
	}{
		{"--lib", append([]string{"nextest", "run", "-p", "m", "--lib"}, filter...),
			append([]string{"nextest", "run", "-p", "m"}, filter...), true},
		{"--test with its value", append([]string{"nextest", "run", "-p", "m", "--test", "integration"}, filter...),
			append([]string{"nextest", "run", "-p", "m"}, filter...), true},
		{"--test=value", append([]string{"nextest", "run", "-p", "m", "--test=integration"}, filter...),
			append([]string{"nextest", "run", "-p", "m"}, filter...), true},
		{"no target", append([]string{"nextest", "run", "-p", "m"}, filter...), nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := dropCargoTarget(Runner{Cmd: "cargo", Args: c.args})
			if ok != c.ok || (ok && !slices.Equal(got.Args, c.out)) {
				t.Fatalf("dropCargoTarget(%v) = %v, %v; want %v, %v", c.args, got.Args, ok, c.out, c.ok)
			}
		})
	}
}
