package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/docs"
)

// CI parity. A gate that runs a different set of checks from the job which
// decides whether the branch is green is a gate that lets the branch go red
// somewhere else, after the commit, where nobody is watching. So a Go root
// answers to the same two checks CI runs — `go vet ./...` and
// `golangci-lint run ./...` — before the suite, in cost order: vet compiles
// nothing beyond what a build already does, lint is a full analysis pass,
// and the suite builds, links and runs.
//
// golangci-lint is not part of any toolchain, so a box without it must not
// have its commits refused over a tool it never installed. An absent binary
// is ONE `lint-skipped` line in gate.log and the commit proceeds; a linter
// that IS there and finds something rejects.

const golangciLint = "golangci-lint"

// lookLinter reports whether the linter is installed. It is a var so a test
// can state "absent" without rewriting PATH, which would also hide go and
// git from the very stages under test.
var lookLinter = func() bool {
	_, err := exec.LookPath(golangciLint)
	return err == nil
}

// goQualityStage runs the CI-parity checks for one Go root, stopping at the
// first rejection.
func goQualityStage(gateName, root string, run SuiteRunner) GateResult {
	vet := Runner{Cmd: "go", Args: []string{"vet", "./..."}, Dir: root}
	if res := goCheckStage(gateName, "vet", root, vet, run); res.Blocked {
		return res
	}
	if !lookLinter() {
		fmt.Fprintf(os.Stderr, "gate %s: %s is not installed — lint skipped in %s\n", gateName, golangciLint, root)
		appendGateLog(gateName, root, golangciLint+" run ./...", "lint-skipped", 0)
		return GateResult{}
	}
	lint := Runner{Cmd: golangciLint, Args: []string{"run", "./..."}, Dir: root}
	return goCheckStage(gateName, "lint", root, lint, run)
}

// goCheckStage runs one check and turns it into a verdict. A timeout fails
// OPEN, exactly as the suite stages do: a stopwatch is not a finding.
func goCheckStage(gateName, stage, root string, r Runner, run SuiteRunner) GateResult {
	res := run(r, root)
	switch {
	case res.TimedOut:
		fmt.Fprintf(os.Stderr, "gate %s: %s in %s → TIMEOUT (FAIL-OPEN — not checked)\n", gateName, stage, root)
		return GateResult{}
	case !res.Passed:
		fmt.Fprintf(os.Stderr, "gate %s: %s in %s → blocked\n", gateName, stage, root)
		appendGateLog(gateName, root, cmdString(r), stage+"-blocked", res.Duration)
		var b strings.Builder
		fmt.Fprintf(&b, "TDD quality: %s failed in %s — fix before committing.\n", stage, root)
		fmt.Fprintf(&b, "command: %s\n", cmdString(r))
		if first := firstDiagnostic(res.Output); first != "" {
			fmt.Fprintf(&b, "first diagnostic: %s\n", first)
		} else if res.Err != "" {
			fmt.Fprintf(&b, "runner error: %s\n", res.Err)
		}
		b.WriteString(tailSnippet(res.Output))
		return GateResult{Blocked: true, Message: b.String()}
	default:
		fmt.Fprintf(os.Stderr, "gate %s: %s in %s → clean\n", gateName, stage, root)
		return GateResult{}
	}
}

// docsCheckMarker is the opt-in file a non-Go repo drops at its root to ask
// for the doc-citation stage.
const docsCheckMarker = ".aphrollo/docs-check"

// docsCheckEnabled reports whether this repo asked for the doc-citation
// stage: a Go module (aphrollo's own CI already runs it) or the marker file.
// Doc conventions are not universal, so a repo that said neither is left
// alone.
func docsCheckEnabled(repoRoot string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(docsCheckMarker))); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(repoRoot, "go.mod"))
	return err == nil
}

// docsCheckStage judges the markdown this commit STAGES: every repo-relative
// path a staged `*.md` cites must resolve. A dangling citation misdrives
// every session that loads the file, and a docs-only commit stages no source
// at all — so this runs before the has-code gate rather than inside a
// per-root suite stage, which such a commit never reaches.
func docsCheckStage(gateName, repoRoot string) GateResult {
	if repoRoot == "" || !docsCheckEnabled(repoRoot) {
		return GateResult{}
	}
	var md []string
	for _, f := range stagedFiles(repoRoot) {
		rel := filepath.ToSlash(f)
		if strings.HasSuffix(strings.ToLower(rel), ".md") {
			md = append(md, rel)
		}
	}
	if len(md) == 0 {
		return GateResult{}
	}
	findings, err := docs.CheckFiles(repoRoot, md)
	if err != nil {
		// A file the check could not read is a defect in the check, not in
		// the commit: say so and let the commit through.
		line := fmt.Sprintf("gate %s: docs → skipped (%v)", gateName, err)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, repoRoot, "docs check", "docs-skipped", 0)
		return GateResult{Message: line}
	}
	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "gate %s: docs → clean (%d file(s))\n", gateName, len(md))
		appendGateLog(gateName, repoRoot, "docs check", "docs-clean", 0)
		return GateResult{}
	}
	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		lines = append(lines, f.String())
	}
	appendGateLog(gateName, repoRoot, "docs check", "docs-rejected", 0)
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate %s: docs → REJECTED\n  %s\n  a cited path must resolve; fix the link or add the file",
		gateName, strings.Join(lines, "\n  "))}
}
