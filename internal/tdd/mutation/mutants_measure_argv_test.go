package mutation

import (
	"strings"
	"testing"
)

// nextest's filterset language has no `ignored()` predicate, so "these
// ignored tests and no others" cannot be said on a command line: any
// --run-ignored switch runs every OTHER ignored test in the workspace too,
// and `#[ignore]` carries at least three unrelated meanings in the consuming
// repo. A repo that needs an environment-bound tier measured selects it
// through its own `[profile.mutants]` default-filter, which the runner
// reaches with NEXTEST_PROFILE — never here (criterion 8a).
func TestMutantsArgv_NeverRunsIgnoredTests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  MutantsConfig
	}{
		{"every key declared", MutantsConfig{
			AtMerge:         true,
			Env:             []string{"BORLD_GPU=1"},
			BaselineExclude: []string{"test(conditioner_burst) # wall-clock under load"},
			Accept:          []string{"crates/a/src/lib.rs:1:36 replace + with - # kind=equivalent: same value"},
			After:           "tools/after.sh",
		}},
		{"nothing declared", MutantsConfig{}},
	} {
		expr, _, _ := mutationBaselineExcludeParse(tc.cfg.BaselineExclude)
		argv := MutantsArgv("/w/changed.diff", 120, []string{"a"}, expr)

		for _, arg := range argv {
			for _, banned := range []string{"--run-ignored", "--ignored", "--include-ignored"} {
				if arg == banned || strings.HasPrefix(arg, banned) {
					t.Errorf("%s: argv carries %q: %v", tc.name, arg, argv)
				}
			}
		}
		var filtersets []string
		for i, arg := range argv {
			if arg == "-E" && i+1 < len(argv) {
				filtersets = append(filtersets, argv[i+1])
			}
		}
		var want []string
		if len(tc.cfg.BaselineExclude) > 0 {
			want = []string{"not(test(conditioner_burst))"}
		}
		if strings.Join(filtersets, "|") != strings.Join(want, "|") {
			t.Errorf("%s: filtersets = %v, want %v", tc.name, filtersets, want)
		}
	}
}

// The half of #691 the issue left unmeasured: whether the diff-scoped
// measurement shares the proof's false SURVIVOR, where a `--lib` selection
// cannot reach a killing test in an integration binary.
//
// It does not, and the measurement says so itself. On a crate whose mutated
// function in src/ is constrained ONLY by a test in tests/, `aphrollo gate
// mutants run` reported 11 tested, 11 caught, 0 missed; with that one
// integration file moved aside and nothing else changed, the same run
// reported 11 tested, 0 caught, 11 missed. The kills came from the
// integration binary, so the selection reaches it.
//
// The reason is this argv: cargo-mutants runs the package's whole nextest
// suite, and nothing here narrows which TARGETS of it are built or run. The
// test pins that, because the cheapest way to reintroduce the defect on this
// side would be to "scope" the measurement the way the proof scoped itself.
func TestMutantsArgv_NeverNarrowsTheTestTargets(t *testing.T) {
	t.Parallel()
	// Every cargo/nextest flag that selects a SUBSET of a package's test
	// targets. `--test-tool=nextest` is not one of them, which is why the
	// comparison is on the flag name rather than on a prefix.
	narrowing := map[string]bool{
		"--lib": true, "--bins": true, "--bin": true, "--doc": true,
		"--test": true, "--tests": true, "--example": true, "--examples": true,
		"--bench": true, "--benches": true,
	}
	argv := MutantsArgv("/w/changed.diff", 120, []string{"forge_driveline"}, "")
	for _, arg := range argv {
		if narrowing[flagName(arg)] {
			t.Errorf("the measurement narrows its test targets with %q, so a mutant killed only by another "+
				"target would read as a survivor (#691): %v", arg, argv)
		}
	}
}
