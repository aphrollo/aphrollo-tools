package precommit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
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
// with `protect --staged`. The installed version is told by `gitleaks version`.
func TestSecretScanArgs_PicksTheStagedFormTheInstalledVersionKnows(t *testing.T) {
	flags := []string{"--staged", "--redact", "--no-banner", "--log-level", "error", "--report-format", "json", "--report-path", "-"}
	cases := []struct {
		name, version, config string
		want                  []string
	}{
		{"new", "8.30.1", "", append([]string{"git"}, flags...)},
		{"first with git", "8.19.0", "", append([]string{"git"}, flags...)},
		{"old", "8.18.4", "", append([]string{"protect"}, flags...)},
		{"unnamed version is taken as current", "", "", append([]string{"git"}, flags...)},
		{"with the repo's config", "8.30.1", "/r/.gitleaks.toml", append(append([]string{"git"}, flags...), "--config", "/r/.gitleaks.toml")},
	}
	for _, c := range cases {
		if got := secretScanArgs(c.version, c.config); !slices.Equal(got, c.want) {
			t.Errorf("%s: args = %q, want %q", c.name, got, c.want)
		}
	}
}

// What gitleaks prints for a finding is read for its file, line, rule and the
// fingerprint .gitleaksignore takes.
func TestSecretScanParse_ReadsFileLineAndRuleFromTheJSONReport(t *testing.T) {
	out := `[ {"RuleID":"generic-api-key","StartLine":7,"File":"x/y.go","Secret":"REDACTED","Fingerprint":"x/y.go:generic-api-key:7"},
	         {"RuleID":"aws-access-token","StartLine":2,"File":"z.txt"} ]`
	got, err := secretScanParse([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []secretFinding{{File: "x/y.go", Line: 7, Rule: "generic-api-key", Fingerprint: "x/y.go:generic-api-key:7"}, {File: "z.txt", Line: 2, Rule: "aws-access-token"}}
	if !slices.Equal(got, want) {
		t.Errorf("findings = %+v, want %+v", got, want)
	}
	if _, err := secretScanParse([]byte("not json")); err == nil {
		t.Error("an unreadable report must be an error, not no findings")
	}
}

// The refusal tells how to clear a false positive without weakening the gate:
// all three ways, the fingerprint to paste, and the scanner's version, with a
// line when it is not the one CI pins.
func TestSecretScan_TheRefusalNamesTheThreeWaysToClearAFindingAndTheVersions(t *testing.T) {
	root := secretscanRepo(t)
	write(t, root, ".github/workflows/pipeline.yml", "run: curl https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz\n")
	secretscanStub(t, root, secretScanResult{Version: "8.18.4",
		Findings: []secretFinding{{File: "b.txt", Line: 1, Rule: "generic-api-key", Fingerprint: "b.txt:generic-api-key:1"}}})

	res := secretScanStage("precommit", root)
	for _, want := range []string{"fingerprint b.txt:generic-api-key:1", "gitleaks:allow", "comment syntax", ".gitleaksignore", "[allowlist]", ".gitleaks.toml",
		"gitleaks 8.18.4", "CI pins gitleaks 8.30.1"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal lacks %q:\n%s", want, res.Message)
		}
	}
}

func TestSecretScan_TheVersionCIPinsIsNoMismatch(t *testing.T) {
	root := secretscanRepo(t)
	write(t, root, ".github/workflows/pipeline.yml", "run: curl https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz\n")
	secretscanStub(t, root, secretScanResult{Version: "8.30.1", Findings: []secretFinding{{File: "b.txt", Line: 1, Rule: "r"}}})
	if res := secretScanStage("precommit", root); strings.Contains(res.Message, "CI pins") {
		t.Errorf("the version CI pins is no mismatch:\n%s", res.Message)
	}
}

// --- the real scan against a fake gitleaks ---------------------------------
//
// The test binary itself stands in for gitleaks when it is run under that name
// (secretscanFakeMain, called first by TestMain): it logs its argv and answers
// as FAKE_GL_MODE says. It is found on PATH the way the real one is.

// secretscanFakeMain runs the fake scanner when this process was started as
// gitleaks, and says whether it did.
func secretscanFakeMain() (code int, ran bool) {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])))
	if name != "gitleaks" {
		return 0, false
	}
	if log := os.Getenv("FAKE_GL_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(os.Getenv("FAKE_GL_VERSION"))
		return 0, true
	}
	switch os.Getenv("FAKE_GL_MODE") {
	case "garbage":
		fmt.Print("this is not json")
		fmt.Fprintln(os.Stderr, "fatal: boom")
		return 1, true
	case "finding":
		fmt.Print(`[{"RuleID":"generic-api-key","StartLine":4,"File":"b.txt","Fingerprint":"b.txt:generic-api-key:4"}]`)
		return 1, true
	case "hang":
		select {} // until the gate ends it
	}
	return 0, true
}

// secretscanFake installs the fake as the first gitleaks on PATH and returns the
// path of its argv log. Tests using it set the environment and so run alone.
func secretscanFake(t *testing.T, mode string) (log string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	// Only an .exe suffix names the program on PATH; a unix test binary is
	// called precommit.test, and "gitleaks.test" is no gitleaks to LookPath.
	suffix := ""
	if strings.EqualFold(filepath.Ext(exe), ".exe") {
		suffix = filepath.Ext(exe)
	}
	bin := t.TempDir()
	if err := proc.WriteExecutable(filepath.Join(bin, "gitleaks"+suffix), body, 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GL_MODE", mode)
	t.Setenv("FAKE_GL_LOG", log)
	t.Setenv("FAKE_GL_VERSION", "8.30.1")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	return log
}

func secretscanCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// Exit 1 is also how gitleaks reports its own failures. Output that is not a
// report is a scanner that could not scan, never a finding.
func TestSecretScanReal_ExitOneWithGarbageIsNotRunNotRejected(t *testing.T) {
	root := secretscanRepo(t)
	log := secretscanFake(t, "garbage")

	res := secretScanStage("precommit", root)
	if res.Blocked || !strings.Contains(res.Message, "NOT RUN") || strings.Contains(res.Message, "REJECTED") {
		t.Errorf("garbage on exit 1 must be NOT RUN, got %+v", res)
	}
	secretscanRanTheFake(t, log, res.Message)
}

// secretscanRanTheFake fails a test whose NOT RUN came from the fake never
// being found rather than from its answer: a missing scanner is NOT RUN too.
func secretscanRanTheFake(t *testing.T, log, message string) {
	t.Helper()
	if strings.Contains(message, "not on PATH") || len(secretscanCalls(t, log)) < 2 {
		t.Fatalf("the fake gitleaks was never run (calls %q): %s", secretscanCalls(t, log), message)
	}
}

func TestSecretScanReal_ExitOneWithAFindingRefusesWithItsFingerprint(t *testing.T) {
	root := secretscanRepo(t)
	secretscanFake(t, "finding")

	res := secretScanStage("precommit", root)
	if !res.Blocked {
		t.Fatalf("a reported finding must refuse: %+v", res)
	}
	for _, want := range []string{"b.txt:4", "generic-api-key", "fingerprint b.txt:generic-api-key:4", "gitleaks 8.30.1"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal lacks %q:\n%s", want, res.Message)
		}
	}
}

func TestSecretScanReal_AScannerThatOutlastsItsTimeIsNotRun(t *testing.T) {
	root := secretscanRepo(t)
	log := secretscanFake(t, "hang")
	prev := secretScanTimeout
	secretScanTimeout = 500 * time.Millisecond
	t.Cleanup(func() { secretScanTimeout = prev })

	res := secretScanStage("precommit", root)
	if res.Blocked || !strings.Contains(res.Message, "NOT RUN") {
		t.Errorf("a timed-out scan must be NOT RUN, got %+v", res)
	}
	secretscanRanTheFake(t, log, res.Message)
}

// The repo's own config goes to the scanner as CI's scan reads it, and the
// version is asked once: the second commit spawns the scanner only to scan.
func TestSecretScanReal_PassesTheRepoConfigAndAsksTheVersionOnce(t *testing.T) {
	root := secretscanRepo(t)
	write(t, root, ".gitleaks.toml", "title = \"t\"\n")
	log := secretscanFake(t, "clean")

	for range 2 {
		if res := secretScanStage("precommit", root); res.Blocked || res.Message != "" {
			t.Fatalf("clean scan: %+v", res)
		}
	}
	calls := secretscanCalls(t, log)
	if len(calls) != 3 {
		t.Fatalf("spawns = %q, want one version call then two scans", calls)
	}
	if calls[0] != "version" {
		t.Errorf("first spawn = %q, want the version call", calls[0])
	}
	for _, scan := range calls[1:] {
		if !strings.HasPrefix(scan, "git --staged") || !strings.Contains(scan, "--config "+filepath.Join(root, ".gitleaks.toml")) {
			t.Errorf("scan argv = %q, want git --staged with the repo's .gitleaks.toml", scan)
		}
	}
}
