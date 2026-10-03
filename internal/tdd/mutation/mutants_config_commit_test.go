package mutation

import (
	"testing"
	"time"
)

// mutants-at-commit is a plain switch: on or off. "ci" is not a spelling of
// it, since the commit-time run is the local gate's own measurement.
func TestMutantsConfig_AtCommitSpellings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"absent", "undercover = true\n", false},
		{"true", "mutants-at-commit = true\n", true},
		{"false", "mutants-at-commit = false\n", false},
		{"a trailing comment", "mutants-at-commit = true # measured at commit\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readModeConfig(t, tc.body)
			if err != nil {
				t.Fatalf("ReadMutantsConfig: %v", err)
			}
			if cfg.AtCommit != tc.want {
				t.Errorf("AtCommit = %v, want %v", cfg.AtCommit, tc.want)
			}
		})
	}
}

func TestMutantsConfig_AtCommitRefusesAnyOtherValue(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`"ci"`, `"yes"`, "1"} {
		_, err := readModeConfig(t, "mutants-at-commit = "+value+"\n")
		if err == nil {
			t.Errorf("mutants-at-commit = %s: no error, want a refusal", value)
		}
	}
	_, err := readModeConfig(t, "mutants-at-commit = \"ci\"\n")
	want := `mutants-at-commit must be true, false, "report" or "block", got "ci"`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// The wall-clock budget is a positive whole number of seconds; absent is a
// half a minute, and the number is read verbatim at its lowest legal value.
func TestMutantsConfig_CommitBudget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want time.Duration
	}{
		{"absent", "mutants-at-commit = true\n", 30 * time.Second},
		{"one second, the lowest legal value", "mutants-commit-budget = 1\n", time.Second},
		{"two seconds", "mutants-commit-budget = 2\n", 2 * time.Second},
		{"a trailing comment", "mutants-commit-budget = 90 # a slow box\n", 90 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readModeConfig(t, tc.body)
			if err != nil {
				t.Fatalf("ReadMutantsConfig: %v", err)
			}
			if got := cfg.CommitBudget(); got != tc.want {
				t.Errorf("CommitBudget() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMutantsConfig_CommitBudgetRefusesWhatIsNotAPositiveNumber(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "-1", "abc", "1.5"} {
		_, err := readModeConfig(t, "mutants-commit-budget = "+value+"\n")
		if err == nil {
			t.Errorf("mutants-commit-budget = %s: no error, want a refusal", value)
		}
	}
}

// A zero-value config, which is what a caller with no repo config holds, still
// answers the default rather than a zero budget that would measure nothing.
func TestMutantsConfig_CommitBudgetOfTheZeroValue(t *testing.T) {
	t.Parallel()
	if got := (MutantsConfig{}).CommitBudget(); got != 30*time.Second {
		t.Errorf("CommitBudget() = %s, want 1m0s", got)
	}
	if got := (MutantsConfig{CommitBudgetSeconds: 1}).CommitBudget(); got != time.Second {
		t.Errorf("CommitBudget() with 1 = %s, want 1s", got)
	}
}
