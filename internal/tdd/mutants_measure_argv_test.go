package tdd

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
