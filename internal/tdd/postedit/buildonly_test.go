package postedit

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// The second shape of the same false green, verbatim from the field:
//
//	cargo nextest run -p engine_audio --example sweep -> green (0 tests — nothing to run, 1.4s)
//
// after an edit to examples/sweep.rs. Widening cannot help this one: an
// --example target and a --no-run bench select zero tests BY CONSTRUCTION,
// always, and there is nothing to widen to. They are COMPILE checks, and the
// gate must say so in its own words rather than borrow green's.

const exampleCompiledOutput = tddtest.ExampleCompiledOutput

// TestNarrow_ExampleIsBuiltNotRun pins the inconsistency the field report
// exposed: --bench already carries --no-run for exactly the stated reason (a
// run costs minutes and says nothing about correctness), and --example — a
// binary with a main(), not test code — did not. Both are compile checks and
// both are now spelled that way.
func TestNarrow_ExampleIsBuiltNotRun(t *testing.T) {
	t.Parallel()
	root := cargoCrate(t, "movement")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "examples", "nubis", "capture.rs"), root)
	args := strings.Join(got.Args, " ")
	if !strings.Contains(args, "--example nubis") || !strings.Contains(args, "--no-run") {
		t.Fatalf("args = %q, want the example BUILT, not run", args)
	}
}

// TestPostEdit_ExampleEdit_ReportsBuildOnlyNeverGreen pins the state itself:
// a compile check names itself, says the code was NOT tested, and never
// borrows the "green (N passed)" or "green (0 tests — nothing to run)" shape
// a session reads as evidence.
func TestPostEdit_ExampleEdit_ReportsBuildOnlyNeverGreen(t *testing.T) {
	cases := []struct {
		name   string
		target string
		res    SuiteResult
	}{
		// nextest RUNS an --example target's binary rather than building it,
		// and exits 4 with "no tests to run" — the verbatim field case,
		// which reached treatAsEmptyPass and printed the empty-crate green.
		{"an example nextest exited 4 over", "examples/sweep.rs", SuiteResult{Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput}},
		{"an example that only compiled", "examples/sweep.rs", SuiteResult{Passed: true, Output: exampleCompiledOutput}},
		{"a bench built with --no-run", "benches/mix.rs", SuiteResult{Passed: true, Output: exampleCompiledOutput}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			root := mkCargoCrate(t, "engine_audio")
			write(t, root, "benches/mix.rs", "fn main() {}\n")
			withNextest(t, root)

			got := PostEdit(postPayload("Edit", root+"/"+c.target), fakeRunResult(c.res))

			if strings.Contains(got, "green") {
				t.Fatalf("a build-only run must never read as green, got: %s", got)
			}
			for _, want := range []string{strings.ToUpper(BuildOnly), "NOT tested"} {
				if !strings.Contains(got, want) {
					t.Fatalf("the advisory must name %q, got: %s", want, got)
				}
			}
			if logged := gateLogText(t, cfg); !strings.Contains(logged, BuildOnly) {
				t.Fatalf("gate.log must carry the %s verdict, got:\n%s", BuildOnly, logged)
			}
		})
	}
}

// TestPostEdit_BuildOnlyRun_LeavesAHandRunAllowed pins the third
// consequence: a compile check is not a test verdict, so it must not arm
// decideNarrowedSuite's 30-minute block over the suite that would actually
// test the crate.
func TestPostEdit_BuildOnlyRun_LeavesAHandRunAllowed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)

	PostEdit(postPayload("Edit", root+"/examples/sweep.rs"),
		fakeRunResult(SuiteResult{Passed: true, Output: exampleCompiledOutput}))

	if isSettledVerdict(BuildOnly) {
		t.Fatalf("%q must not read as a settled verdict — a compile check tests nothing", BuildOnly)
	}
	d := decideBash(t, "s1", root, "cargo nextest run -p engine_audio --lib -E test(/^defs::/)")
	if d.Action != Allow {
		t.Fatalf("a hand-run after a build-only verdict must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// TestPostEdit_ExampleThatFailsToCompile_IsStillRed guards the direction the
// build-only state must NOT launder: a compile check that FAILS is a real
// failure about the code just edited, and stays red.
func TestPostEdit_ExampleThatFailsToCompile_IsStillRed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)

	broken := SuiteResult{Passed: false, Err: "exit status 101",
		Output: "error[E0425]: cannot find function `sweep` in this scope\n --> examples/sweep.rs:2:5\n"}
	got := PostEdit(postPayload("Edit", root+"/examples/sweep.rs"), fakeRunResult(broken))

	if strings.Contains(got, strings.ToUpper(BuildOnly)) {
		t.Fatalf("a failed compile must not be reported as a clean build-only run, got: %s", got)
	}
	if !strings.Contains(got, "outcome=red") {
		t.Fatalf("a failed compile must stay RED, got: %s", got)
	}
}

// fakeRunResult is fakeRun's fuller sibling: a SuiteRunner that yields one
// prepared SuiteResult (Err and TimedOut included) for every runner it is
// handed.
func fakeRunResult(res SuiteResult) SuiteRunner {
	return func(Runner, string) SuiteResult { return res }
}
