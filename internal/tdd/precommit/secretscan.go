package precommit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// The secret-scan stage. CI's `scan` job runs gitleaks over every commit a PR
// adds, so a secret-looking line that passed every commit stage failed there
// and, since each commit is scanned, only a rewrite of the lane's history
// cleared it (#1255). The commit gate now runs the same scanner over the staged
// diff, ahead of the suites: a finding refuses the commit with file:line, the
// rule and the escape.
//
// gitleaks is not part of any toolchain, so a box without it is not refused: it
// gets one NOT RUN line naming the tool, never a silent pass. A scanner that
// fails or outlasts its time gets the same line with the reason. The repo's own
// .gitleaks.toml is passed when it exists, as CI's scan reads it.
//
// The pre-merge gate does not run it. A merge has no staged diff to scan; its
// counterpart is gitleaks over the whole PR range, which CI's `scan` job already
// runs on that exact range for every PR and which costs a history walk here.

const (
	secretScanTool    = "gitleaks"
	secretScanConfig  = ".gitleaks.toml"
	secretScanStageID = "secrets"
	secretScanTimeout = 30 * time.Second
	secretScanEscape  = "append `// gitleaks:allow` to the line if it is a fixture, otherwise remove the secret"
)

// secretFinding is one line the scanner flagged.
type secretFinding struct {
	File string
	Line int
	Rule string
}

// secretScanResult is what one scan answered: the findings, or that the tool is
// missing, or why it could not answer.
type secretScanResult struct {
	Findings []secretFinding
	Missing  bool
	Err      error
}

// secretScanAt is the scan a test states for its own root, so it runs beside
// the rest; with nothing registered the real scanner runs.
var secretScanAt rootseam.Table[func(root string) secretScanResult]

func secretScanFor(root string) secretScanResult {
	if fn, ok := secretScanAt.Get(root); ok {
		return fn(root)
	}
	return scanStagedSecrets(root)
}

// secretScanStage scans the staged diff of repoRoot.
func secretScanStage(gateName, repoRoot string) GateResult {
	if repoRoot == "" || len(stagedFiles(repoRoot)) == 0 {
		return verdictFor(gateName, secretScanStageID, repoRoot, secretScanTool, stageOutcome{Kind: outcomePass})
	}
	res := secretScanFor(repoRoot)
	notRun := func(why string) GateResult {
		return verdictFor(gateName, secretScanStageID, repoRoot, secretScanTool, stageOutcome{Kind: outcomeSkipped,
			Reason: "NOT RUN — " + why + "; the staged diff was not scanned and this pass is not a green for it"})
	}
	switch {
	case res.Missing:
		return notRun(secretScanTool + " is not on PATH; install it so the gate can scan")
	case res.Err != nil:
		return notRun(secretScanTool + " could not scan: " + res.Err.Error())
	case len(res.Findings) == 0:
		AppendGateLog(gateName, repoRoot, secretScanTool, "secrets-clean", 0)
		return verdictFor(gateName, secretScanStageID, repoRoot, secretScanTool, stageOutcome{Kind: outcomePass})
	}
	lines := make([]string, 0, len(res.Findings))
	for _, f := range res.Findings {
		lines = append(lines, fmt.Sprintf("%s:%d  %s", f.File, f.Line, f.Rule))
	}
	return verdictFor(gateName, secretScanStageID, repoRoot, secretScanTool, stageOutcome{Kind: outcomeFail,
		Message: fmt.Sprintf("gate %s: secrets → REJECTED (%d finding(s) in the staged diff)\n  %s\n  %s",
			gateName, len(res.Findings), strings.Join(lines, "\n  "), secretScanEscape)})
}

// scanStagedSecrets runs gitleaks over the staged diff of root.
func scanStagedSecrets(root string) secretScanResult {
	bin, err := exec.LookPath(secretScanTool)
	if err != nil {
		return secretScanResult{Missing: true}
	}
	help, _ := run.LightCombined(run.Spec{Name: bin, Args: []string{"git", "--help"}, Dir: root, Timeout: secretScanTimeout})
	config := ""
	if p := filepath.Join(root, secretScanConfig); pathExists(p) {
		config = p
	}
	out, err := run.LightOutput(run.Spec{Name: bin, Args: secretScanArgs(string(help), config), Dir: root, Timeout: secretScanTimeout})
	var exit *exec.ExitError
	switch {
	case err == nil:
		return secretScanResult{}
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// Exit 1 is also how gitleaks reports its own fatal errors; only a
		// readable report of findings is a refusal.
		findings, perr := secretScanParse(out)
		if perr != nil || len(findings) == 0 {
			return secretScanResult{Err: fmt.Errorf("exit 1 with no readable report: %s", strings.TrimSpace(string(exit.Stderr)))}
		}
		return secretScanResult{Findings: findings}
	}
	return secretScanResult{Err: err}
}

// secretScanArgs is the staged-diff scan for the installed gitleaks: 8.19 and
// later have `git --staged`, older ones `protect --staged`. help is what
// `gitleaks git --help` printed; config is the repo's config path or "".
func secretScanArgs(help, config string) []string {
	verb := "protect"
	if strings.Contains(help, "--staged") {
		verb = "git"
	}
	args := []string{verb, "--staged", "--redact", "--no-banner", "--log-level", "error", "--report-format", "json", "--report-path", "-"}
	if config != "" {
		args = append(args, "--config", config)
	}
	return args
}

// secretScanParse reads gitleaks' JSON report.
func secretScanParse(out []byte) ([]secretFinding, error) {
	var raw []struct {
		RuleID    string
		StartLine int
		File      string
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("unreadable gitleaks report: %w", err)
	}
	findings := make([]secretFinding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, secretFinding{File: filepath.ToSlash(r.File), Line: r.StartLine, Rule: r.RuleID})
	}
	return findings, nil
}
