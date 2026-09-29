package mutation

import (
	"path/filepath"
	"strings"
	"testing"
)

func readModeConfig(t *testing.T, body string) (MutantsConfig, error) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\n"+body)
	return ReadMutantsConfig(root)
}

// mutants-at-merge and mutants-before-pr take true, false or "ci". "ci" is on
// for the judge (the repo declares a measurement) and CI-only for the local
// gate.
func TestMutantsConfig_ModeSpellings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                 string
		body                 string
		atMerge, atMergeCI   bool
		beforePR, beforePRCI bool
	}{
		{"absent", "undercover = true\n", false, false, false, false},
		{"true", "mutants-at-merge = true\nmutants-before-pr = true\n", true, false, true, false},
		{"false", "mutants-at-merge = false\nmutants-before-pr = false\n", false, false, false, false},
		{"ci", "mutants-at-merge = \"ci\"\nmutants-before-pr = \"ci\"\n", true, true, true, true},
		{"ci at merge only", "mutants-at-merge = \"ci\"\n", true, true, false, false},
		{"ci before the PR only", "mutants-before-pr = \"ci\"\n", false, false, true, true},
		{"a trailing comment", "mutants-at-merge = \"ci\" # measured by mutants-verdict\n", true, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readModeConfig(t, tc.body)
			if err != nil {
				t.Fatalf("ReadMutantsConfig: %v", err)
			}
			if cfg.AtMerge != tc.atMerge || cfg.AtMergeCI != tc.atMergeCI {
				t.Errorf("at-merge = (%v, ci %v), want (%v, ci %v)", cfg.AtMerge, cfg.AtMergeCI, tc.atMerge, tc.atMergeCI)
			}
			if cfg.BeforePR != tc.beforePR || cfg.BeforePRCI != tc.beforePRCI {
				t.Errorf("before-pr = (%v, ci %v), want (%v, ci %v)", cfg.BeforePR, cfg.BeforePRCI, tc.beforePR, tc.beforePRCI)
			}
		})
	}
}

// A value that is none of the three is refused: a repo that wrote it believes
// it is measured.
func TestMutantsConfig_RefusesAnUnknownMode(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"mutants-at-merge", "mutants-before-pr"} {
		_, err := readModeConfig(t, key+" = \"cii\"\n")
		want := key + ` must be true, false or "ci", got "cii"`
		if err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", key, err, want)
		}
	}
}

// The integration list is read as declared: empty when absent, one entry,
// several.
func TestMutantsConfig_IntegrationPackages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"absent", "undercover = true\n", ""},
		{"one", "mutants-integration-packages = [\"internal/cli\"]\n", "internal/cli"},
		{"two", "mutants-integration-packages = [\n  \"internal/cli\",\n  \"cmd/aphrollo\",\n]\n", "cmd/aphrollo,internal/cli"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readModeConfig(t, tc.body)
			if err != nil {
				t.Fatalf("ReadMutantsConfig: %v", err)
			}
			if got := strings.Join(cfg.IntegrationPackages, ","); got != tc.want {
				t.Errorf("IntegrationPackages = %q, want %q", got, tc.want)
			}
		})
	}
}
