package mutation

import (
	"strings"
	"testing"
)

// A measurement reports by default; only a repo that pins "block" has its
// findings refuse anything.
func TestMutantsConfig_LevelSpellings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                    string
		body                    string
		atCommit, atCommitBlock bool
		atMergeBlock            bool
	}{
		{"absent", "undercover = true\n", false, false, false},
		{"commit true reports", "mutants-at-commit = true\n", true, false, false},
		{"commit report", "mutants-at-commit = \"report\"\n", true, false, false},
		{"commit block", "mutants-at-commit = \"block\"\n", true, true, false},
		{"commit block with comment", "mutants-at-commit = \"block\" # pinned\n", true, true, false},
		{"commit false", "mutants-at-commit = false\n", false, false, false},
		{"merge level report", "mutants-at-merge = \"ci\"\nmutants-at-merge-level = \"report\"\n", false, false, false},
		{"merge level block", "mutants-at-merge = \"ci\"\nmutants-at-merge-level = \"block\"\n", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readModeConfig(t, tc.body)
			if err != nil {
				t.Fatalf("ReadMutantsConfig: %v", err)
			}
			if cfg.AtCommit != tc.atCommit || cfg.AtCommitBlock != tc.atCommitBlock || cfg.AtMergeBlock != tc.atMergeBlock {
				t.Errorf("got AtCommit=%v AtCommitBlock=%v AtMergeBlock=%v, want %v %v %v",
					cfg.AtCommit, cfg.AtCommitBlock, cfg.AtMergeBlock, tc.atCommit, tc.atCommitBlock, tc.atMergeBlock)
			}
		})
	}
}

func TestMutantsConfig_MergeLevelRefusesAnyOtherValue(t *testing.T) {
	t.Parallel()
	_, err := readModeConfig(t, "mutants-at-merge-level = \"strict\"\n")
	want := `mutants-at-merge-level must be "report" or "block", got "strict"`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// ratchet: test_removed TestMutantsConfig_CommitBudgetDefaultsToThirtySeconds: renamed to TestMutantsConfig_CommitBudgetDefaultsToNinetySeconds when the default moved to 90 s
// The commit-time run is short by design: ninety seconds unless the repo says
// otherwise.
func TestMutantsConfig_CommitBudgetDefaultsToNinetySeconds(t *testing.T) {
	t.Parallel()
	if got := (MutantsConfig{}).CommitBudget().Seconds(); got != 90 {
		t.Errorf("default budget = %vs, want 90s", got)
	}
}

var survivingVerdict = Verdict{Refused: true, Missed: 1, Tested: 2, Unaccepted: []MutantOutcome{{File: "a/b.go", Line: 3, Col: 4}}, Message: "a/b.go:3:4: CONDITIONALS_NEGATION\nmutants: 1 survived"}

func TestJudgeLevel_AReportOnlyRepoIsToldAndNotRefused(t *testing.T) {
	t.Parallel()
	v := ApplyMergeLevel(MutantsConfig{AtMerge: true, AtMergeCI: true}, survivingVerdict)
	if v.Refused {
		t.Fatalf("a repo that did not pin block was refused: %s", v.Message)
	}
	for _, want := range []string{"a/b.go:3:4: CONDITIONALS_NEGATION", "REPORT ONLY", "mutants-at-merge-level"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message lacks %q:\n%s", want, v.Message)
		}
	}
	if v.Missed != 1 {
		t.Errorf("Missed = %d, want the finding's count kept", v.Missed)
	}
}

func TestJudgeLevel_APinnedRepoStaysRefused(t *testing.T) {
	t.Parallel()
	v := ApplyMergeLevel(MutantsConfig{AtMerge: true, AtMergeBlock: true}, survivingVerdict)
	if !v.Refused || v.Message != survivingVerdict.Message {
		t.Errorf("verdict = %+v, want the refusal untouched", v)
	}
}

func TestJudgeLevel_ACleanVerdictIsUntouched(t *testing.T) {
	t.Parallel()
	clean := Verdict{Tested: 3, Caught: 3, Message: "all caught"}
	if got := ApplyMergeLevel(MutantsConfig{AtMerge: true}, clean); got.Refused || got.Message != clean.Message || got.Caught != 3 {
		t.Errorf("verdict = %+v, want %+v", got, clean)
	}
}

// A refusal that is no finding about a mutant (shards missing, an accept-list
// that cannot be read) is a broken measurement, and a broken measurement
// refuses at every level.
func TestJudgeLevel_ABrokenMeasurementStaysRefused(t *testing.T) {
	t.Parallel()
	broken := Verdict{Refused: true, Message: "mutants: the shard reports are not the whole measurement"}
	if v := ApplyMergeLevel(MutantsConfig{AtMerge: true}, broken); !v.Refused || v.Message != broken.Message {
		t.Errorf("verdict = %+v, want the refusal untouched", v)
	}
}
