package precommit

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// secretscanRepo is a repo with one staged file, the shape the stage reads.
func secretscanRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "b.txt", "secret_key=\"x\"\n")
	gitDo(t, root, "add", ".")
	return root
}

func secretscanStub(t *testing.T, root string, res secretScanResult) *int {
	t.Helper()
	calls := 0
	t.Cleanup(secretScanAt.Set(root, func(string) secretScanResult { calls++; return res }))
	return &calls
}

// PR #1253 passed every commit stage and failed CI's gitleaks scan, so the
// history had to be rewritten. A finding now refuses the commit and names the
// file:line, the rule and the escape (#1255).
func TestSecretScan_AFindingRefusesTheCommitNamingLineRuleAndTheEscape(t *testing.T) {
	root := secretscanRepo(t)
	secretscanStub(t, root, secretScanResult{Findings: []secretFinding{{File: "b.txt", Line: 1, Rule: "generic-api-key"}}})

	res := secretScanStage("precommit", root)
	if !res.Blocked {
		t.Fatalf("a finding must refuse the commit: %+v", res)
	}
	for _, want := range []string{"b.txt:1", "generic-api-key", "gitleaks:allow"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal lacks %q:\n%s", want, res.Message)
		}
	}
}

// A box without the scanner must not refuse commits over a tool it never
// installed, and must not pass silently either: one NOT RUN line names it.
func TestSecretScan_NoScannerIsOneNotRunLineNeverARefusalNorASilentPass(t *testing.T) {
	root := secretscanRepo(t)
	secretscanStub(t, root, secretScanResult{Missing: true})

	res := secretScanStage("precommit", root)
	if res.Blocked {
		t.Fatalf("a missing scanner must not refuse the commit: %s", res.Message)
	}
	if !strings.Contains(res.Message, "NOT RUN") || !strings.Contains(res.Message, "gitleaks") {
		t.Errorf("want a NOT RUN line naming gitleaks, got %q", res.Message)
	}
	if strings.Count(res.Message, "\n") > 0 {
		t.Errorf("want one line, got %q", res.Message)
	}
}

// A scanner that fails or runs out of time proved nothing: NOT RUN with the
// reason, not a pass and not a refusal of a commit that may be clean.
func TestSecretScan_AScannerThatFailsIsNotRunWithTheReason(t *testing.T) {
	root := secretscanRepo(t)
	secretscanStub(t, root, secretScanResult{Err: errors.New("timed out after 30s")})

	res := secretScanStage("precommit", root)
	if res.Blocked || !strings.Contains(res.Message, "NOT RUN") || !strings.Contains(res.Message, "timed out after 30s") {
		t.Errorf("want an unblocked NOT RUN line with the reason, got %+v", res)
	}
}

func TestSecretScan_ACleanDiffPassesWithNoMessage(t *testing.T) {
	root := secretscanRepo(t)
	secretscanStub(t, root, secretScanResult{})
	if res := secretScanStage("precommit", root); res.Blocked || res.Message != "" {
		t.Errorf("clean must be silent and unblocked, got %+v", res)
	}
}

// With nothing staged there is no diff to scan, so the scanner is never started.
func TestSecretScan_NothingStagedNeverStartsTheScanner(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	calls := secretscanStub(t, root, secretScanResult{Missing: true})
	if res := secretScanStage("precommit", root); res.Blocked || res.Message != "" {
		t.Errorf("nothing staged must say nothing, got %+v", res)
	}
	if *calls != 0 {
		t.Errorf("scanner started %d time(s) with nothing staged", *calls)
	}
}

// The stage sits in the commit gate itself, before any suite is built: a commit
// with a secret is refused without the suite running.
func TestPrecommit_ASecretIsRefusedBeforeAnySuiteRuns(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "go.mod", "module m\n\ngo 1.26\n")
	write(t, root, "a.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "a.go", "package m\n\nconst k = 1\n")
	gitDo(t, root, "add", ".")
	secretscanStub(t, root, secretScanResult{Findings: []secretFinding{{File: "a.go", Line: 3, Rule: "generic-api-key"}}})

	ran := 0
	res := Precommit(root, func(Runner, string) SuiteResult { ran++; return SuiteResult{Passed: true} })
	if !res.Blocked || !strings.Contains(res.Message, "a.go:3") {
		t.Fatalf("want the commit refused with the finding, got %+v", res)
	}
	if ran != 0 {
		t.Errorf("%d suite run(s) started before the secret scan refused", ran)
	}
}

// gitleaks 8.19 and later scan a staged diff with `git --staged`; older ones
// with `protect --staged`. The installed version is told by its own help.
func TestSecretScanArgs_PicksTheStagedFormTheInstalledVersionKnows(t *testing.T) {
	cases := []struct {
		name, help, config string
		want               []string
	}{
		{"new", "Usage:\n  gitleaks git [flags]\n      --staged   scan staged commits\n", "",
			[]string{"git", "--staged", "--redact", "--no-banner", "--log-level", "error", "--report-format", "json", "--report-path", "-"}},
		{"old", "Usage:\n  gitleaks [command]\n", "",
			[]string{"protect", "--staged", "--redact", "--no-banner", "--log-level", "error", "--report-format", "json", "--report-path", "-"}},
		{"with the repo's config", "--staged", "/r/.gitleaks.toml",
			[]string{"git", "--staged", "--redact", "--no-banner", "--log-level", "error", "--report-format", "json", "--report-path", "-", "--config", "/r/.gitleaks.toml"}},
	}
	for _, c := range cases {
		if got := secretScanArgs(c.help, c.config); !slices.Equal(got, c.want) {
			t.Errorf("%s: args = %q, want %q", c.name, got, c.want)
		}
	}
}

// What gitleaks prints for a finding is read for its file, line and rule.
func TestSecretScanParse_ReadsFileLineAndRuleFromTheJSONReport(t *testing.T) {
	out := `[ {"RuleID":"generic-api-key","StartLine":7,"File":"x/y.go","Secret":"REDACTED"},
	         {"RuleID":"aws-access-token","StartLine":2,"File":"z.txt"} ]`
	got, err := secretScanParse([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []secretFinding{{File: "x/y.go", Line: 7, Rule: "generic-api-key"}, {File: "z.txt", Line: 2, Rule: "aws-access-token"}}
	if !slices.Equal(got, want) {
		t.Errorf("findings = %+v, want %+v", got, want)
	}
	if _, err := secretScanParse([]byte("not json")); err == nil {
		t.Error("an unreadable report must be an error, not no findings")
	}
}
