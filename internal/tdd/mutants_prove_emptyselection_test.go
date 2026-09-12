package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proveOverASelection drives a full mutation proof over a real cargo crate
// whose suite run came back in the given shape, and hands back the exit code,
// everything the proof said, and the runner it actually ran. The mutation
// itself is real: the pattern matches once, the write lands, git sees the
// diff — only what the RUN reports is canned, which is the whole question
// here.
func proveOverASelection(t *testing.T, res SuiteResult) (int, string, Runner) {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	src := "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n"
	write(t, root, filepath.FromSlash("src/lib.rs"), src)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	var ran Runner
	fakeRun := func(r Runner, _ string) SuiteResult {
		ran = r
		return res
	}

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "src", "lib.rs"),
		Old:      "a + b",
		New:      "a - b",
		WantFail: "add_sums_its_arguments",
	}, fakeRun, &out, &errb)

	if got, err := os.ReadFile(filepath.Join(root, "src", "lib.rs")); err != nil {
		t.Fatal(err)
	} else if string(got) != src {
		t.Fatalf("the file was not restored byte-identically: %q", got)
	}
	return code, out.String() + errb.String(), ran
}

// The half of #637 that outranks the filter bug it came from: a mutation
// proof whose run selected ZERO tests reported the mutant as a SURVIVOR. The
// mutation had landed on disk, the suite "stayed green", and the verdict said
// the tests do not constrain that line — while nothing had been tested at
// all. That is the reading most likely to be believed and acted on, and
// acting on it means writing a test that already exists.
//
// The post-edit half of the gate already refuses to call such a run green
// (#642); the proof path did not get that treatment, and this is it. Every
// shape below is one a cargo runner actually prints.
func TestRunMutantsProve_RefusesWhenTheRunSelectedZeroTests(t *testing.T) {
	cases := []struct {
		name string
		res  SuiteResult
	}{
		{
			// `cargo test --lib some_module::` with a filter that matches
			// nothing: exit 0, a clean green summary, not one test run.
			name: "cargo test filtered every test out and exited 0",
			res: SuiteResult{Passed: true, Output: "running 0 tests\n\n" +
				"test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 57 filtered out; finished in 0.00s\n"},
		},
		{
			// nextest's own answer to an empty selection: exit 4, which
			// arrives here as a NON-passing run carrying no failing test.
			name: "nextest refused an empty selection with exit 4",
			res:  SuiteResult{Passed: false, Output: "error: no tests to run\n"},
		},
		{
			// The same thing when the runner is configured not to fail on it:
			// a summary that ran nothing.
			name: "nextest summarised a run of zero tests",
			res: SuiteResult{Passed: true, Output: "    Starting 0 tests across 9 binaries (137 skipped)\n" +
				"     Summary [   0.001s] 0 tests run: 0 passed, 137 skipped\n"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, report, ran := proveOverASelection(t, c.res)

			if code == ExitMutantsProveSurvived {
				t.Fatalf("a run that selected no tests was reported as a SURVIVOR:\n%s", report)
			}
			if code == ExitMutantsProveKilled {
				t.Fatalf("a run that selected no tests was reported as a KILL:\n%s", report)
			}
			if code != ExitMutantsProveNoTestsSelected {
				t.Fatalf("exit = %d, want ExitMutantsProveNoTestsSelected (%d):\n%s",
					code, ExitMutantsProveNoTestsSelected, report)
			}
			if !strings.Contains(strings.ToLower(report), NoTestsSelected) {
				t.Fatalf("the refusal never names itself %s:\n%s", NoTestsSelected, report)
			}
			// It must name the filter it used, or the reader cannot tell
			// WHICH selection came back empty — the one fact that turns the
			// refusal into a fix.
			if !strings.Contains(report, cmdString(ran)) {
				t.Fatalf("the refusal never names the command/filter it ran (%s):\n%s", cmdString(ran), report)
			}
		})
	}
}

// The discrimination that keeps the refusal honest: a run that genuinely
// executed tests and stayed green is still a REAL survivor, reported exactly
// as before. A refusal that swallowed this case would hide every survivor
// there is.
func TestRunMutantsProve_AGreenRunThatActuallyRanTestsIsStillASurvivor(t *testing.T) {
	code, report, _ := proveOverASelection(t, SuiteResult{
		Passed: true,
		Output: "running 3 tests\ntest tests::add_sums_its_arguments ... ok\n\n" +
			"test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.10s\n",
	})
	if code != ExitMutantsProveSurvived {
		t.Fatalf("exit = %d, want ExitMutantsProveSurvived (%d):\n%s", code, ExitMutantsProveSurvived, report)
	}
	if strings.Contains(strings.ToLower(report), NoTestsSelected) {
		t.Fatalf("a run of three passing tests was reported as an empty selection:\n%s", report)
	}
}

// A timed-out run already has its own verdict, and how many tests its
// selection WOULD have run is unknown — the refusal must not overwrite one
// honest inconclusive answer with another, differently wrong one.
func TestRunMutantsProve_ATimeoutIsStillATimeoutNotAnEmptySelection(t *testing.T) {
	code, report, _ := proveOverASelection(t, SuiteResult{
		TimedOut: true,
		Output:   "running 0 tests\n",
	})
	if code != ExitMutantsProveTimedOut {
		t.Fatalf("exit = %d, want ExitMutantsProveTimedOut (%d):\n%s", code, ExitMutantsProveTimedOut, report)
	}
}
