package postedit

import (
	"strings"
	"testing"
)

// Issue #730, verbatim from the field:
//
//	cargo nextest run -p forge --lib -E test(/^soft_car::read::/) → NO-TESTS-SELECTED
//
// after an accessor rename across a crate whose tests live outside the
// renamed module. The hook told the session to run the crate suites by
// hand, and it did (`cargo nextest run -p forge_solver -p forge`, 1497
// passed) — a test run the hook should have made itself. A narrowed run
// that selects nothing climbs a ladder, module filter → the crate's lib →
// the whole crate, each rung only when the one below it selected nothing.

// TestCargoWideningSteps_ClimbFromModuleFilterToLibToWholeCrate pins the
// ladder itself, in both dialects. A rung that would repeat a lower one is
// never offered: a `--test` target with no name filter has no lib rung, and a
// run already as wide as the crate goes has no rung at all.
func TestCargoWideningSteps_ClimbFromModuleFilterToLibToWholeCrate(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"nextest module filter", []string{"nextest", "run", "-p", "forge", "--lib", "-E", "test(/^soft_car::read::/)"},
			[]string{"nextest run -p forge --lib", "nextest run -p forge"}},
		{"cargo test substring filter", []string{"test", "-p", "forge", "--lib", "soft_car::read::"},
			[]string{"test -p forge --lib", "test -p forge"}},
		{"an inline filter expression", []string{"nextest", "run", "-p", "forge", "--lib", "--filter-expr=test(/^a::/)"},
			[]string{"nextest run -p forge --lib", "nextest run -p forge"}},
		{"a --test target without a filter", []string{"nextest", "run", "-p", "forge", "--test", "lab"},
			[]string{"nextest run -p forge"}},
		{"a bare --lib run", []string{"nextest", "run", "-p", "forge", "--lib"},
			[]string{"nextest run -p forge"}},
		{"already crate-wide", []string{"nextest", "run", "-p", "forge"}, nil},
		{"a build-only example", []string{"nextest", "run", "-p", "forge", "--example", "sweep", "--no-run"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps := cargoWideningSteps(Runner{Cmd: "cargo", Args: c.args, Dir: "/ws"})
			var got []string
			for _, s := range steps {
				got = append(got, strings.Join(s.Args, " "))
				if s.Dir != "/ws" {
					t.Fatalf("a rung must keep the workspace dir, got %q", s.Dir)
				}
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("ladder for %v = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// TestPostEdit_DirectWideningPastTheBudget_NamesTheRungItCouldNotRun pins the
// budget half on the foreground path: widening shares the edit's ONE budget,
// and a rung that budget cannot start is named as not run, inconclusive. It
// must never turn the narrowed run's empty selection into a green, and it
// must never quietly start a run the hook has no time left to wait for.
func TestPostEdit_DirectWideningPastTheBudget_NamesTheRungItCouldNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	root := mkCargoCrate(t, "engine_audio")
	withNextest(t, root)

	var seen []string
	got := PostEdit(postPayload("Edit", root+"/src/defs.rs"), scriptedRunner(t, &seen, map[string]SuiteResult{
		"cargo nextest run -p engine_audio --lib -E test(/^defs::/)": {Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput},
	}))

	if len(seen) != 1 {
		t.Fatalf("a spent budget must not start the next rung, ran %v", seen)
	}
	if strings.Contains(got, "green") {
		t.Fatalf("an empty selection must never read as green, got: %s", got)
	}
	for _, want := range []string{"cargo nextest run -p engine_audio --lib", "NOT tested", "budget"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the line must name %q, got: %s", want, got)
		}
	}
}
