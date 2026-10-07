package precommit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// rule, the fingerprint and the three ways to clear a false positive. The
// refusal holds for an agent and a person alike; there is no session waiver.
//
// gitleaks is not part of any toolchain, so a box without it is not refused: it
// gets one NOT RUN line naming the tool, never a silent pass. A scanner that
// fails or outlasts its time gets the same line with the reason. The repo's own
// .gitleaks.toml is passed when it exists, as CI's scan reads it.
//
// One scan is spawned per commit: the staged form (`git --staged`, 8.19 and
// later, or `protect --staged`) is chosen from `gitleaks version`, which is read
// once per binary and kept in the state dir by the binary's path and mtime.
//
// The pre-merge gate does not run it. A merge has no staged diff to scan; its
// counterpart is gitleaks over the whole PR range, which CI's `scan` job already
// runs on that exact range for every PR and which costs a history walk here.

const (
	secretScanTool        = "gitleaks"
	secretScanConfig      = ".gitleaks.toml"
	secretScanStageID     = "secrets"
	secretScanVersionFile = "gitleaks-version.json"
	secretScanCIWorkflow  = ".github/workflows/pipeline.yml"
)

// secretScanTimeout bounds each gitleaks spawn; a var so a test can shorten it.
var secretScanTimeout = 30 * time.Second

// secretFinding is one line the scanner flagged.
type secretFinding struct {
	File string
	Line int
	Rule string
	// Fingerprint is what .gitleaksignore takes to clear this one finding.
	Fingerprint string
}

// secretScanResult is what one scan answered: the findings, or that the tool is
// missing, or why it could not answer. Version is the scanner's own, "" when it
// did not say.
type secretScanResult struct {
	Findings []secretFinding
	Missing  bool
	Err      error
	Version  string
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

// secretScanClearing is the three ways a false positive is cleared, in the words
// the refusal prints.
const secretScanClearing = `to clear a false positive, any one of:
    1. put gitleaks:allow in a comment on that line, in the file's own comment syntax (// gitleaks:allow, # gitleaks:allow, <!-- gitleaks:allow -->)
    2. add the finding's fingerprint, printed above, to .gitleaksignore (one per line)
    3. add an [allowlist] to .gitleaks.toml
  otherwise remove the secret`

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
		line := fmt.Sprintf("%s:%d  %s", f.File, f.Line, f.Rule)
		if f.Fingerprint != "" {
			line += "  fingerprint " + f.Fingerprint
		}
		lines = append(lines, line)
	}
	local := "unknown version"
	if res.Version != "" {
		local = res.Version
	}
	notes := "  scanner: " + secretScanTool + " " + local
	if ci := ciGitleaksVersion(repoRoot); ci != "" && ci != res.Version {
		notes += fmt.Sprintf("\n  note: CI pins gitleaks %s (pipeline.yml), not this version; a finding or its absence may not reproduce there", ci)
	}
	return verdictFor(gateName, secretScanStageID, repoRoot, secretScanTool, stageOutcome{Kind: outcomeFail,
		Message: fmt.Sprintf("gate %s: secrets → REJECTED (%d finding(s) in the staged diff)\n  %s\n%s\n  %s",
			gateName, len(res.Findings), strings.Join(lines, "\n  "), notes, secretScanClearing)})
}

var ciGitleaksRe = regexp.MustCompile(`releases/download/v(\d+\.\d+\.\d+)/gitleaks`)

// ciGitleaksVersion is the gitleaks version CI's pipeline downloads, "" when the
// repo carries no such workflow or names none.
func ciGitleaksVersion(repoRoot string) string {
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(secretScanCIWorkflow)))
	if err != nil {
		return ""
	}
	if m := ciGitleaksRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// scanStagedSecrets runs gitleaks over the staged diff of root.
func scanStagedSecrets(root string) secretScanResult {
	bin, err := exec.LookPath(secretScanTool)
	if err != nil {
		return secretScanResult{Missing: true}
	}
	version := gitleaksVersion(bin, root)
	config := ""
	if p := filepath.Join(root, secretScanConfig); pathExists(p) {
		config = p
	}
	out, err := run.LightOutput(run.Spec{Name: bin, Args: secretScanArgs(version, config), Dir: root, Timeout: secretScanTimeout})
	var exit *exec.ExitError
	switch {
	case err == nil:
		return secretScanResult{Version: version}
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// Exit 1 is also how gitleaks reports its own fatal errors; only a
		// readable report of findings is a refusal.
		findings, perr := secretScanParse(out)
		if perr != nil || len(findings) == 0 {
			return secretScanResult{Version: version, Err: fmt.Errorf("exit 1 with no readable report: %s", strings.TrimSpace(string(exit.Stderr)))}
		}
		return secretScanResult{Version: version, Findings: findings}
	}
	return secretScanResult{Version: version, Err: err}
}

// secretScanVersionCache is what the state dir keeps of one `gitleaks version`.
type secretScanVersionCache struct {
	Path    string `json:"path"`
	MTime   int64  `json:"mtime"`
	Version string `json:"version"`
}

// gitleaksVersion is the version bin reports, asked once per binary: the answer
// is kept in the state dir under the binary's path and mtime, so a reinstall
// asks again. "" when it did not name one (a build without a version).
func gitleaksVersion(bin, root string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	mtime := info.ModTime().UnixNano()
	cache := ""
	if dir := StateDir(); dir != "" {
		cache = filepath.Join(dir, secretScanVersionFile)
	}
	if cache != "" {
		if b, err := os.ReadFile(cache); err == nil {
			var c secretScanVersionCache
			if json.Unmarshal(b, &c) == nil && samePath(c.Path, bin) && c.MTime == mtime {
				return c.Version
			}
		}
	}
	out, _ := run.LightOutput(run.Spec{Name: bin, Args: []string{"version"}, Dir: root, Timeout: secretScanTimeout})
	version := semverRe.FindString(string(out))
	if cache != "" {
		if b, err := json.Marshal(secretScanVersionCache{Path: bin, MTime: mtime, Version: version}); err == nil {
			if os.MkdirAll(filepath.Dir(cache), 0o755) == nil {
				_ = os.WriteFile(cache, b, 0o644)
			}
		}
	}
	return version
}

// secretScanArgs is the staged-diff scan for the installed gitleaks: 8.19 and
// later have `git --staged`, older ones `protect --staged`. A version that was
// not named is taken as current. config is the repo's config path or "".
func secretScanArgs(version, config string) []string {
	verb := "git"
	var major, minor, patch int
	if n, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); n == 3 && (major < 8 || (major == 8 && minor < 19)) {
		verb = "protect"
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
		RuleID      string
		StartLine   int
		File        string
		Fingerprint string
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("unreadable gitleaks report: %w", err)
	}
	findings := make([]secretFinding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, secretFinding{File: filepath.ToSlash(r.File), Line: r.StartLine, Rule: r.RuleID, Fingerprint: r.Fingerprint})
	}
	return findings, nil
}
