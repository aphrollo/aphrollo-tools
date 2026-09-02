package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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

// linterVersion reports the local binary's version ("" when it cannot be
// read). A var so a test can state a version without installing one.
var linterVersion = func(dir string) string {
	cmd := exec.Command(golangciLint, "--version")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return semverRe.FindString(string(out))
}

var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// workflowPinRe reads the version CI installs out of the workflow file, which
// is the only place the pin actually lives.
var workflowPinRe = regexp.MustCompile(`golangci-lint@v(\d+\.\d+\.\d+)`)

// driftNoted dedupes the drift line to once per process per version pair: a
// commit touching three Go roots must not say the same thing three times.
var driftNoted sync.Map

// goQualityStage runs the CI-parity checks for one Go root, stopping at the
// first rejection. repoRoot is where the workflow file lives, which is not
// necessarily the Go root inside a monorepo.
func goQualityStage(gateName, repoRoot, root string, run SuiteRunner) GateResult {
	vet := Runner{Cmd: "go", Args: []string{"vet", "./..."}, Dir: root}
	if res := goCheckStage(gateName, "vet", root, vet, run); res.Blocked {
		return res
	}
	if !lookLinter() {
		fmt.Fprintf(os.Stderr, "gate %s: %s is not installed — lint skipped in %s\n", gateName, golangciLint, root)
		appendGateLog(gateName, root, golangciLint+" run ./...", "lint-skipped", 0)
		return GateResult{}
	}
	noteLintVersionDrift(gateName, repoRoot, root)
	// --allow-serial-runners: golangci-lint takes a MACHINE-WIDE lock, not one
	// per cache dir, so a second one anywhere on the box makes this one exit 3
	// with "parallel golangci-lint is running" — a rejection that says nothing
	// about the code. CI passes it for the same reason; the gate, which runs
	// while other sessions build, needs it more.
	lint := Runner{Cmd: golangciLint, Args: []string{"run", "--allow-serial-runners", "./..."}, Dir: root}
	return goCheckStage(gateName, "lint", root, lint, run)
}

// noteLintVersionDrift says, once, that the local linter is not the one CI
// pins. It never rejects: a version mismatch is not a defect in the code, and
// refusing the commit would wedge every box that has not upgraded. But it is
// exactly the failure this stage exists to prevent — a green commit followed
// by a red CI job — so it is not silent either.
func noteLintVersionDrift(gateName, repoRoot, root string) {
	pinned := pinnedLinterVersion(repoRoot)
	if pinned == "" {
		return
	}
	local := linterVersion(root)
	if local == "" || local == pinned {
		return
	}
	key := pinned + "\x00" + local
	if _, seen := driftNoted.LoadOrStore(key, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr, "gate %s: %s is %s locally, CI pins %s — running anyway; the two can disagree\n",
		gateName, golangciLint, local, pinned)
	appendGateLog(gateName, root, golangciLint+" "+local+" vs "+pinned, "lint-version-drift", 0)
}

// pinnedLinterVersion reads the version the workflow installs. "" when there
// is no workflow, or it names none — a repo with no CI pin has nothing to
// drift from.
func pinnedLinterVersion(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	dir := filepath.Join(repoRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if m := workflowPinRe.FindStringSubmatch(string(data)); m != nil {
			return m[1]
		}
	}
	return ""
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
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
		return true
	}
	// A cargo workspace says it in the manifest, beside every other gate
	// opt-in, rather than growing a second place to look.
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	return cargoAphrolloFlag(ws, "docs-check")
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
