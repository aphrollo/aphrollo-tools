package postedit

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Issue #759: deleting an unused-looking const from a source file that
// another SOURCE file still reads fails the build with rustc's E0425, the
// same "cannot find value" a test naming a missing function produces. The
// hook called it red-missing-impl and printed "Clean RED — write the minimum
// implementation" for an edit that had no test in it at all. A clean RED is
// a claim about a test; the compiler says where the missing name is used,
// and only a use in test code earns the claim.

// probeCrate writes the #759 shape into a cargo repo: a `mode_probe` module
// whose `project` reads a const from its sibling `probe`, with project's
// inline tests directly below the production function. probe.rs holds
// whatever the test passes; project.rs likewise.
func probeCrate(t *testing.T, probe, project string) string {
	t.Helper()
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, filepath.Join("src", "lib.rs"), "pub mod mode_probe;\n")
	write(t, root, filepath.Join("src", "mode_probe.rs"), "pub mod probe;\npub mod project;\n")
	write(t, root, filepath.Join("src", "mode_probe", "probe.rs"), probe)
	write(t, root, filepath.Join("src", "mode_probe", "project.rs"), project)
	return root
}

// rustcOutput builds root's lib tests with the real toolchain and returns what
// cargo printed, which must be a compile failure. It skips when cargo is not
// installed; the compiler is never faked, because the location lines this
// judgement reads are rustc's own format.
//
// The package as a whole isolates CARGO_HOME to a fixture-only directory with
// no real cargo under it (main_test.go), so every OTHER `cargo` this package
// might spawn stays a pure fixture operation. This helper wants the real
// toolchain instead, so it opts back in with useRealCargoHome — without it,
// `cargo` still resolves on PATH (it is the box's queue shim, never absent),
// but the shim itself fails to resolve a real cargo under the isolated
// CARGO_HOME and prints ITS OWN "resolve cargo: ... not found" error. That
// text is not a compile failure at all: LookPath succeeds so the skip never
// fires, ExtractFailingTests/missingImplRe find nothing in it, and the run
// falls through to a plain Red regardless of what the fixture's source
// actually named — silently never exercising rustc.
func rustcOutput(t *testing.T, root string) string {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever cargo is installed.
		t.Skip("cargo not on PATH; skipping the real-compiler case")
	}
	useRealCargoHome(t)
	cmd := exec.Command("cargo", "test", "--lib", "--no-run", "--offline")
	cmd.Dir = root
	cmd.Env = append(cleanGitEnv(), "CARGO_TARGET_DIR="+t.TempDir(), "CARGO_TERM_COLOR=never")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the fixture must fail to compile, cargo printed:\n%s", out)
	}
	return string(out)
}

// A production function reading a const nobody declares any more, with the
// crate's inline tests starting on the very next line.
const projectReadsLimit = "pub fn limit() -> u32 {\n" +
	"    crate::mode_probe::probe::LIMIT\n" +
	"}\n" +
	"#[cfg(test)]\n" +
	"mod tests {\n" +
	"    #[test]\n" +
	"    fn limit_is_three() {\n" +
	"        assert_eq!(super::limit(), 3);\n" +
	"    }\n" +
	"}\n"

func TestPostEditFile_ASourceEditThatBreaksAnotherSourceFileIsRedNotMissingImpl(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := probeCrate(t, "pub const UNUSED: u32 = 1;\n", projectReadsLimit)
	failed := SuiteResult{Passed: false, Output: rustcOutput(t, root)}
	run := func(Runner, string) SuiteResult { return failed }

	text, _ := postEditFile("s759a", filepath.Join(root, "src", "mode_probe", "probe.rs"), run)

	want := []string{string(Red)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v; advisory:\n%s", got, want, text)
	}
}

// The harvest of a deferred phase judges the same output the same way: it is
// where a heavy crate's verdict actually lands.
func TestEditResultAdvisory_ASourceEditThatBreaksAnotherSourceFileIsRedNotMissingImpl(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := probeCrate(t, "pub const UNUSED: u32 = 1;\n", projectReadsLimit)
	log := filepath.Join(t.TempDir(), "phase.log")
	if err := os.WriteFile(log, []byte(rustcOutput(t, root)), 0o600); err != nil {
		t.Fatal(err)
	}
	j := DeferredJob{
		Project: root, Phase: "build", Dir: root, Log: log,
		Runner: []string{"cargo", "test", "--lib", "--no-run"},
		File:   filepath.Join(root, "src", "mode_probe", "probe.rs"),
	}

	text := editResultAdvisory(j, PhaseOutcome{ExitCode: 101}, root, nil, "", "")

	want := []string{string(Red)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this harvest, want %v; advisory:\n%s", got, want, text)
	}
}

// The same-hook deferred path: the build phase finished inside the budget
// and failed, so its log is judged before the hook returns.
func TestPostEdit_DeferredBuildThatBreaksAnotherSourceFileIsRedNotMissingImpl(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := probeCrate(t, "pub const UNUSED: u32 = 1;\n", projectReadsLimit)
	output := rustcOutput(t, root)
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		j.PID = 1000
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Session, j.Project)
		if err := os.WriteFile(j.Log, []byte(output), 0o600); err != nil {
			t.Error(err)
		}
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: 101, Seconds: 1})
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })

	text := PostEdit(postPayload("Edit", filepath.Join(root, "src", "mode_probe", "probe.rs")), fakeRun(false, "unused"))

	want := []string{string(Red)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v; advisory:\n%s", got, want, text)
	}
}

// The clean RED the verdict exists for stays one: an inline test calling a
// function nobody has written yet. The call sits below a blank line inside
// the test module, so where a line starts is counted over every line above.
func TestPostEditFile_AnInlineTestCallingAMissingFunctionStaysMissingImpl(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := "pub fn limit() -> u32 {\n" +
		"    3\n" +
		"}\n" +
		"#[cfg(test)]\n" +
		"mod tests {\n" +
		"    #[test]\n" +
		"    fn the_limit_ramps_to_six() {\n" +
		"\n" +
		"        assert_eq!(super::limit_after_ramp(), 6);\n" +
		"    }\n" +
		"}\n"
	root := probeCrate(t, "pub const UNUSED: u32 = 1;\n", project)
	failed := SuiteResult{Passed: false, Output: rustcOutput(t, root)}
	run := func(Runner, string) SuiteResult { return failed }

	postEditFile("s759b", filepath.Join(root, "src", "mode_probe", "project.rs"), run)

	want := []string{string(RedMissingImpl)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v", got, want)
	}
}

// A sibling file a `#[cfg(test)] #[path]` declaration mounts is test code as
// a whole, though it carries no #[cfg(test)] of its own.
func TestPostEditFile_AMountedTestFileCallingAMissingFunctionStaysMissingImpl(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := "pub fn limit() -> u32 {\n" +
		"    3\n" +
		"}\n" +
		"#[cfg(test)]\n" +
		"#[path = \"project_tests.rs\"]\n" +
		"mod tests;\n"
	root := probeCrate(t, "pub const UNUSED: u32 = 1;\n", project)
	write(t, root, filepath.Join("src", "mode_probe", "project_tests.rs"),
		"#[test]\nfn the_limit_ramps_to_six() {\n    assert_eq!(super::limit_after_ramp(), 6);\n}\n")
	failed := SuiteResult{Passed: false, Output: rustcOutput(t, root)}
	run := func(Runner, string) SuiteResult { return failed }

	postEditFile("s759m", filepath.Join(root, "src", "mode_probe", "project_tests.rs"), run)

	want := []string{string(RedMissingImpl)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v", got, want)
	}
}

// Only a MISSING-SYMBOL diagnostic is attributed: a type error in the tests
// beside a source file's missing const does not turn that const into a
// symbol under test.
func TestPostEditFile_AnotherErrorInTheTestsDoesNotLendItsLocation(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := strings.Replace(projectReadsLimit,
		"        assert_eq!(super::limit(), 3);\n",
		"        let three: u32 = \"three\";\n        assert_eq!(super::limit(), three);\n", 1)
	root := probeCrate(t, "pub const UNUSED: u32 = 1;\n", project)
	failed := SuiteResult{Passed: false, Output: rustcOutput(t, root)}
	run := func(Runner, string) SuiteResult { return failed }

	postEditFile("s759c", filepath.Join(root, "src", "mode_probe", "probe.rs"), run)

	want := []string{string(Red)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v", got, want)
	}
}

// Go states the location at the start of the diagnostic's own line. A
// missing name used from a _test.go file is the clean RED; the same name
// used from production code, or quoted in a failing test's own message, is
// not one.
func TestPostEditFile_GoMissingSymbolIsACleanRedOnlyFromATestFile(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   Outcome
	}{
		{"used from a test file", "# example.com/m\n./widget_test.go:9:6: undefined: NewWidget\nFAIL\texample.com/m [build failed]\n", RedMissingImpl},
		{"used from production code", "# example.com/m\n./widget.go:5:9: undefined: Limit\nFAIL\texample.com/m [build failed]\n", Red},
		{"a test file's other error beside it", "# example.com/m\n./widget.go:5:9: undefined: Limit\n./widget_test.go:9:2: declared and not used: w\nFAIL\texample.com/m [build failed]\n", Red},
		{"quoted in an assertion message", "--- FAIL: TestLookup (0.00s)\n    widget_test.go:40: error = \"undefined: Widget\", want nil\nFAIL\nFAIL\texample.com/m\t0.01s\n", Red},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			root := makeGoRepo(t)
			write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
			run := func(Runner, string) SuiteResult { return SuiteResult{Passed: false, Output: c.output} }

			postEditFile("s759go", filepath.Join(root, "widget.go"), run)

			want := []string{string(c.want)}
			if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
				t.Fatalf("gate.log recorded %v for this run, want %v", got, want)
			}
		})
	}
}
