package postedit

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// The false green this file exists for, verbatim from the field:
//
//	cargo nextest run -p engine_audio --lib -E test(/^defs::/) -> green (0 tests — nothing to run, 1.3s)
//
// after an edit to src/defs.rs in a crate whose substantive tests are
// INTEGRATION tests under tests/. The narrowed run selected nothing, nextest
// exited 4, treatAsEmptyPass turned that into a PASS, and the session read a
// green for code no test had touched. Every post-edit hook in that crate
// produced it, so a whole feature's worth of RED-first evidence was
// unavailable from the gate and both builders fell back to hand mutation
// proofs.
//
// The rule these tests pin: zero selected under a NARROWING means "this
// filter selected nothing", never "this crate has no tests" — two different
// facts the gate used to print the same line for.

// mkCargoCrate writes a real crate layout — an inline #[cfg(test)] module
// under src/ AND a separate integration test binary under tests/, the exact
// shape that produces the false green — and returns its root. The suite is
// always faked (cargo need not be installed): what the layout drives is
// NarrowToRelatedTests, which is pure path logic over these files.
func mkCargoCrate(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \""+name+"\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "src/lib.rs", "pub mod defs;\n")
	write(t, root, "src/defs.rs",
		"pub fn sample_rate() -> u32 { 48_000 }\n"+
			"#[cfg(test)]\nmod tests {\n    #[test]\n    fn sample_rate_is_48k() { assert_eq!(super::sample_rate(), 48_000); }\n}\n")
	write(t, root, "tests/intake.rs", "#[test]\nfn intake_accepts_a_stream() { assert!(true); }\n")
	write(t, root, "examples/sweep.rs", "fn main() {}\n")
	return root
}

// withNextest gives a crate the checked-in nextest config and a fake
// cargo-nextest on PATH, so cargoVerbArgs resolves to the nextest dialect
// (the -E filter expression) rather than plain cargo test's substring.
func withNextest(t *testing.T, root string) {
	t.Helper()
	write(t, root, ".config/nextest.toml", "[profile.default]\n")
	putFakeNextest(t)
}

const nextestSixPassedOutput = tddtest.NextestSixPassedOutput

// cargoTestZeroSelectedOutput is the plain-cargo-test dialect reaching the
// SAME wrong conclusion by a different route: a substring filter that
// matches nothing exits 0 and prints a zero result line, with filtered_out
// naming the tests it excluded.
const cargoTestZeroSelectedOutput = "   Compiling engine_audio v0.1.0 (/w/engine_audio)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.31s\n" +
	"     Running unittests src/lib.rs (target/debug/deps/engine_audio-9a1b2c)\n\n" +
	"running 0 tests\n\n" +
	"test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 3 filtered out; finished in 0.00s\n\n"

// cargoTestThreePassedOutput is that dialect's widened run.
const cargoTestThreePassedOutput = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.02s\n" +
	"     Running tests/intake.rs (target/debug/deps/intake-7f0e)\n\n" +
	"running 3 tests\n" +
	"test intake_accepts_a_stream ... ok\n\n" +
	"test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s\n\n"

// scriptedRunner replies to each Runner by its rendered command line and
// records the order they were invoked in, so a test can assert BOTH what the
// gate ran and that it ran the widened command exactly once. An unscripted
// command fails the test rather than returning a silent zero result.
func scriptedRunner(t *testing.T, seen *[]string, script map[string]SuiteResult) SuiteRunner {
	t.Helper()
	return func(r Runner, _ string) SuiteResult {
		cmd := cmdString(r)
		*seen = append(*seen, cmd)
		res, ok := script[cmd]
		if !ok {
			t.Fatalf("the gate ran an unscripted command: %q", cmd)
		}
		return res
	}
}

// TestPostEdit_NarrowedRunSelectsZero_WidensOnceAndReportsTheWiderVerdict
// pins part one of the fix: a narrowed run that selects nothing climbs the
// widening ladder — the module filter dropped first (the crate's lib tests),
// then the target too (the whole package) — until a run selects a test, and
// that run decides the verdict. The crate's tests exist; they are just not
// where the filter looked. Each rung runs once.
func TestPostEdit_NarrowedRunSelectsZero_WidensOnceAndReportsTheWiderVerdict(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)

	empty := SuiteResult{Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput}
	var seen []string
	script := map[string]SuiteResult{
		"cargo nextest run -p engine_audio --lib -E test(/^defs::/)": empty,
		"cargo nextest run -p engine_audio --lib":                    empty,
		"cargo nextest run -p engine_audio":                          {Passed: true, Output: nextestSixPassedOutput},
	}
	got := PostEdit(postPayload("Edit", root+"/src/defs.rs"), scriptedRunner(t, &seen, script))

	want := []string{
		"cargo nextest run -p engine_audio --lib -E test(/^defs::/)",
		"cargo nextest run -p engine_audio --lib",
		"cargo nextest run -p engine_audio",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("want the narrowed run, then the lib rung, then the package rung, each once:\n%v\ngot:\n%v", want, seen)
	}
	if !strings.Contains(got, "gate: cargo nextest run -p engine_audio in ") {
		t.Fatalf("the gate line must name the command that finally ran, got: %s", got)
	}
	if !strings.Contains(got, "green (6 passed") {
		t.Fatalf("the widened run's verdict must be the one reported, got: %s", got)
	}
	if strings.Contains(got, "0 tests — nothing to run") {
		t.Fatalf("a narrowed zero-selection must never print the empty-crate green, got: %s", got)
	}
	if !strings.Contains(got, "widened") {
		t.Fatalf("the advisory must say the run was widened and why, got: %s", got)
	}
}

// TestPostEdit_PlainCargoTestFilterMatchesNothing_WidensToo pins the same
// fix in the OTHER dialect: moduleFilterArgs' substring branch exits 0 with
// "0 tests" instead of nextest's exit 4, so it reaches the same wrong
// conclusion by a different route and must be widened by the same rule.
func TestPostEdit_PlainCargoTestFilterMatchesNothing_WidensToo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkCargoCrate(t, "engine_audio")

	var seen []string
	script := map[string]SuiteResult{
		"cargo test -p engine_audio --lib defs::": {Passed: true, Output: cargoTestZeroSelectedOutput},
		"cargo test -p engine_audio --lib":        {Passed: true, Output: cargoTestZeroSelectedOutput},
		"cargo test -p engine_audio":              {Passed: true, Output: cargoTestThreePassedOutput},
	}
	got := PostEdit(postPayload("Edit", root+"/src/defs.rs"), scriptedRunner(t, &seen, script))

	if len(seen) != 3 || seen[1] != "cargo test -p engine_audio --lib" || seen[2] != "cargo test -p engine_audio" {
		t.Fatalf("want the lib rung then the package rung after a substring filter matched nothing, got %v", seen)
	}
	if !strings.Contains(got, "green (3 passed") {
		t.Fatalf("the widened run's verdict must be the one reported, got: %s", got)
	}
}

// TestPostEdit_ZeroSelectionEvenWidened_IsInconclusiveNeverGreen pins part
// two: where a selection came back empty the fact is named in its own words
// and joins the INCONCLUSIVE family (TIMEOUT / SKIPPED / QUEUED-SKIPPED /
// deferred-abandoned / infra-failed) — the code was NOT tested — instead of
// being folded into a settled green.
func TestPostEdit_ZeroSelectionEvenWidened_IsInconclusiveNeverGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)

	empty := SuiteResult{Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput}
	var seen []string
	script := map[string]SuiteResult{
		"cargo nextest run -p engine_audio --lib -E test(/^defs::/)": empty,
		"cargo nextest run -p engine_audio --lib":                    empty,
		"cargo nextest run -p engine_audio":                          empty,
	}
	got := PostEdit(postPayload("Edit", root+"/src/defs.rs"), scriptedRunner(t, &seen, script))

	if strings.Contains(got, "green") {
		t.Fatalf("a run that selected no test at all must never read as green, got: %s", got)
	}
	for _, want := range []string{strings.ToUpper(NoTestsSelected), "NOT tested", "tests/"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the advisory must name %q, got: %s", want, got)
		}
	}
}

// TestWidenCargoRunner_DropsTheNarrowingAndKeepsThePackage pins what
// widening IS, in both dialects, and the two runs it must refuse to widen: a
// run with nothing left to drop (already as wide as the crate goes) and a
// build-only target, where widening would trade a compile check for the
// package's whole suite to answer a question nobody asked.
func TestWidenCargoRunner_DropsTheNarrowingAndKeepsThePackage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string // nil: must not widen at all
	}{
		{"nextest lib plus module filter", []string{"nextest", "run", "-p", "engine_audio", "--lib", "-E", "test(/^defs::/)"}, []string{"nextest", "run", "-p", "engine_audio"}},
		{"cargo test lib plus substring", []string{"test", "-p", "engine_audio", "--lib", "defs::"}, []string{"test", "-p", "engine_audio"}},
		{"a --test target is narrowing like any other", []string{"nextest", "run", "-p", "engine_audio", "--test", "intake"}, []string{"nextest", "run", "-p", "engine_audio"}},
		{"already package-wide", []string{"nextest", "run", "-p", "engine_audio"}, nil},
		{"an example is build-only, never widened", []string{"nextest", "run", "-p", "engine_audio", "--example", "sweep", "--no-run"}, nil},
		{"a bench is build-only, never widened", []string{"test", "-p", "engine_audio", "--bench", "mix", "--no-run"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := widenCargoRunner(Runner{Cmd: "cargo", Args: c.args, Dir: "/ws"})
			if c.want == nil {
				if ok {
					t.Fatalf("must not widen %v, got %v", c.args, got.Args)
				}
				return
			}
			if !ok {
				t.Fatalf("must widen %v", c.args)
			}
			if strings.Join(got.Args, " ") != strings.Join(c.want, " ") {
				t.Fatalf("widened %v to %v, want %v", c.args, got.Args, c.want)
			}
			if got.Dir != "/ws" {
				t.Fatalf("the widened run must keep the workspace dir, got %q", got.Dir)
			}
		})
	}
}

// TestPostEdit_ZeroSelection_LeavesANarrowedHandRunAllowed pins part three's
// pairing directly: appendGateLog records the verdict and decideNarrowedSuite
// refuses every hand-run suite for 30 minutes on a SETTLED one, so a
// zero-selection run logged as green silenced exactly the manual run that
// would have caught it. The inconclusive verdict must reach gate.log, and
// isSettledVerdict must not treat it as settled.
func TestPostEdit_ZeroSelection_LeavesANarrowedHandRunAllowed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)

	empty := SuiteResult{Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput}
	var seen []string
	PostEdit(postPayload("Edit", root+"/src/defs.rs"), scriptedRunner(t, &seen, map[string]SuiteResult{
		"cargo nextest run -p engine_audio --lib -E test(/^defs::/)": empty,
		"cargo nextest run -p engine_audio --lib":                    empty,
		"cargo nextest run -p engine_audio":                          empty,
	}))

	if isSettledVerdict(NoTestsSelected) {
		t.Fatalf("%q must not read as a settled verdict — nothing was tested", NoTestsSelected)
	}
	if logged := gateLogText(t, cfg); !strings.Contains(logged, NoTestsSelected) {
		t.Fatalf("gate.log must carry the inconclusive verdict, got:\n%s", logged)
	}
	d := decideBash(t, "s1", root, "cargo nextest run -p engine_audio --lib -E test(/^defs::/)")
	if d.Action != Allow {
		t.Fatalf("a hand-run after a zero-selection verdict must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}
