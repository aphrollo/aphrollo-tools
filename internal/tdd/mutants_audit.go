package tdd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Issue #521 takes the mutation run off every commit and off CI, scoped to
// one lane's own diff. That leaves a gap it names explicitly: a diff-scoped
// run only mutates lines a lane touched, so a test weakened by a change
// AROUND the code it guards generates no mutant, and nothing re-proves it.
//
// `aphrollo gate mutants audit --package <name>` (issue #522) is the
// instrument that closes that gap — a whole-crate (Rust) or whole-package
// (Go) run, reached for on demand when auditing or reviewing a unit, never
// run because a hook fired. It is deliberately NOT wired into precommit,
// premerge or CI, and it shares NONE of the lane pipeline's receipt
// machinery (mutants_run.go, mutants_go.go, receipt.go):
//
//   - A receipt is a claim about ONE tip tree's DIFF, keyed on that tree
//     (MutationReceiptPathFor). A whole-crate run measures strictly more than
//     any lane diff ever would, which is exactly why letting it satisfy the
//     merge gate would launder a proof for a lane it never measured.
//   - So this file never calls MutationReceiptPathFor, signReceipt or
//     writeReceiptFile, and never drives a consuming repo's own
//     tools/mutation_gate.sh (the script that DOES write to that path,
//     unconditionally, keyed on whatever tree it is standing on — running it
//     here, at the same commit a lane might share, would collide with that
//     lane's own receipt file). Instead this file drives the mutation tool
//     ITSELF, directly: `cargo mutants` for Rust, gremlins for Go, exactly
//     the way mutants_go.go already drives gremlins for the Go lane path.
//     TestMutantsAudit_SourceNeverReferencesTheReceiptMachinery pins this.
//   - It still shares the box-wide run lock (acquireMutantsRunLock) and the
//     one mutants worktree area per repo (MutantsRootDir) — a bare `cargo
//     mutants` is refused by the cargo shim outside a target dir shaped like
//     that area, and two whole-crate builds at once OOM rustc the same way
//     two lane builds do — but it runs in ITS OWN subdirectory ("audit"),
//     never a lane's, so preparing it can never reset a tree a lane's
//     producer is still measuring.

// AuditSurvivor is one mutant an audit run found nothing catching, in the
// shape a person reviewing a crate reads — not the shape a gate judges.
type AuditSurvivor struct {
	File     string
	Line     int
	Col      int
	Mutation string
}

// String is "file:line: mutation" (issue #522's own spelling): ranked top to
// bottom by file then line, which is what a reviewer scans, not the column a
// receipt keys survivor identity on.
func (s AuditSurvivor) String() string {
	return fmt.Sprintf("%s:%d: %s", s.File, s.Line, s.Mutation)
}

// AuditReport is what one whole-crate/whole-package run found.
type AuditReport struct {
	Scope     string
	Total     int
	Caught    int
	Survivors []AuditSurvivor
}

// mutantsAuditWorktreeDir is the audit's own tree, beside the lane worktrees
// under the same mutants root but never one of them: a fixed name ("audit"),
// not a lane key, so it can never collide with — and never be reset by —
// chooseMutantsWorktree's own picks for a running lane.
func mutantsAuditWorktreeDir(root string) string {
	r := MutantsRootDir(root)
	if r == "" {
		return ""
	}
	return filepath.Join(r, "audit")
}

// prepareMutantsAuditWorktree checks the audit tree out at root's CURRENT
// HEAD, creating it the first time and hard-resetting it otherwise — the same
// shape prepareMutantsWorktree uses for a lane, minus the job bookkeeping an
// audit run has none of.
func prepareMutantsAuditWorktree(root string) (string, error) {
	dir := mutantsAuditWorktreeDir(root)
	if dir == "" {
		return "", errors.New("could not resolve this repo's mutants area to place the audit worktree")
	}
	tip := strings.TrimSpace(gitOut(root, "rev-parse", "HEAD"))
	if tip == "" {
		return "", errors.New("could not resolve HEAD to audit")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", err
		}
		if out, err := git(root, "worktree", "add", "--detach", dir, tip); err != nil {
			return "", errors.New(strings.TrimSpace(out))
		}
		return dir, nil
	}
	if out, err := git(dir, "reset", "-q", "--hard", tip); err != nil {
		return "", errors.New(strings.TrimSpace(out))
	}
	return dir, nil
}

// auditChildEnv is the driven tool's environment: the shared mutants target
// dir (what gets this invocation PAST the cargo shim's bare-`cargo mutants`
// refusal — it is let through by building into that target dir's shape, not
// by any flag), and the three temp-dir variables pointed under it for the
// identical reason mutantsTempEnv exists on the lane path (a Windows tool
// that ignores TMPDIR and writes tree copies to the OS temp dir has filled a
// drive from 40 GB free to 12 GB before).
func auditChildEnv(targetDir string) []string {
	tmp := filepath.Join(targetDir, "tmp")
	_ = os.MkdirAll(tmp, 0o755)
	drop := map[string]bool{"CARGO_TARGET_DIR": true, "TMPDIR": true, "TMP": true, "TEMP": true}
	out := make([]string, 0, len(os.Environ())+6)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	return append(out,
		"CARGO_TARGET_DIR="+targetDir,
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp,
		"CI=1", "NO_COLOR=1")
}

// outcomesToReport turns a raw outcome list into what a person reads:
// ranked survivors and the totals a caller prints beside them.
func outcomesToReport(scope string, outcomes []MutantOutcome) AuditReport {
	sortOutcomes(outcomes)
	r := AuditReport{Scope: scope, Total: len(outcomes)}
	for _, m := range outcomes {
		switch m.Status {
		case "caught":
			r.Caught++
		case "missed":
			r.Survivors = append(r.Survivors, AuditSurvivor{File: m.File, Line: m.Line, Col: m.Col, Mutation: m.Mutation})
		}
	}
	return r
}

// mutantsAuditRustFn drives cargo-mutants over a WHOLE crate — no --in-diff
// at all, so every mutable line in --package pkg is a candidate — as a seam
// so RunMutantsAudit's own dispatch can be proven without a toolchain.
var mutantsAuditRustFn = runRustAudit

func runRustAudit(root, pkg string, stdout, stderr io.Writer) (AuditReport, error) {
	dir, err := prepareMutantsAuditWorktree(root)
	if err != nil {
		return AuditReport{}, fmt.Errorf("could not prepare the audit worktree: %w", err)
	}
	targetDir := MutantsTargetDir(root)
	if targetDir == "" {
		return AuditReport{}, errors.New("could not resolve this repo's mutants target dir")
	}
	outDir := filepath.Join(dir, "mutants.out")
	// Cleared up front: readMutantsOut below reads whatever is there after
	// the run, and a stale directory from an earlier audit would answer for
	// mutants this run never measured.
	_ = os.RemoveAll(outDir)
	cmd := exec.Command("cargo", "mutants", "--in-place", "--test-tool=nextest", "--package", pkg)
	cmd.Dir = dir
	cmd.Env = auditChildEnv(targetDir)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// cargo-mutants exits non-zero whenever a survivor exists — expected, not
	// a failed RUN. Only the absence of mutants.out (checked below, not the
	// exit code) means the tool never measured anything at all.
	runErr := cmd.Run()
	if _, statErr := os.Stat(outDir); statErr != nil {
		return AuditReport{}, fmt.Errorf("cargo mutants wrote no mutants.out in %s: %v (run error: %v)", dir, statErr, runErr)
	}
	return outcomesToReport(pkg, readMutantsOut(dir)), nil
}

// mutantsAuditGoFn drives gremlins over a WHOLE package — no --diff, so
// gremlins walks scope end to end — the same seam shape as the Rust half.
var mutantsAuditGoFn = runGoAudit

func runGoAudit(root, scope string, stdout, stderr io.Writer) (AuditReport, error) {
	dir, err := prepareMutantsAuditWorktree(root)
	if err != nil {
		return AuditReport{}, fmt.Errorf("could not prepare the audit worktree: %w", err)
	}
	outPath := filepath.Join(dir, "audit-report.json")
	_ = os.Remove(outPath)
	jobs, _ := resolveMutantsJobs(0, false)
	if jobs < 1 {
		jobs = 1
	}
	cmd := exec.Command(gremlinsBin, "unleash", "--silent", "--output", outPath, "--workers", strconv.Itoa(jobs), scope)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		return AuditReport{}, fmt.Errorf("gremlins wrote no report at %s: %v (run error: %v)", outPath, readErr, runErr)
	}
	outcomes, parseErr := parseGremlinsReport(data)
	if parseErr != nil {
		return AuditReport{}, fmt.Errorf("could not read gremlins' report: %w", parseErr)
	}
	return outcomesToReport(scope, outcomes), nil
}

// RunMutantsAudit is `aphrollo gate mutants audit --package <name>`: an
// on-demand whole-crate/whole-package run, never a gate. scope is a crate
// name for a Rust repo (cargo-mutants' own --package) or a package path for
// a Go one (gremlins' own positional path, e.g. "./internal/tdd").
func RunMutantsAudit(dir, scope string, stdout, stderr io.Writer) int {
	if strings.TrimSpace(scope) == "" {
		fmt.Fprintln(stderr, "aphrollo gate mutants audit: --package <name> names the crate (Rust) or package path (Go) to audit")
		return 2
	}
	root := RepoRoot(dir)
	if root == "" {
		fmt.Fprintf(stderr, "aphrollo gate mutants audit: %s is not inside a git repository\n", dir)
		return 1
	}
	targetDir := MutantsTargetDir(root)
	if targetDir == "" {
		fmt.Fprintln(stderr, "aphrollo gate mutants audit: could not resolve this repo's mutants area — git could not answer --git-common-dir here")
		return 1
	}
	if ok, line := mutantsDiskOK(targetDir, 1); !ok {
		fmt.Fprintln(stderr, "aphrollo gate mutants audit: "+line)
		return 1
	}
	ws := cargoWorkspaceRoot(root)
	isRust := fileExists(filepath.Join(ws, "Cargo.toml"))
	isGo := fileExists(filepath.Join(root, "go.mod"))
	if !isRust && !isGo {
		fmt.Fprintf(stderr, "aphrollo gate mutants audit: neither a Cargo.toml nor a go.mod at %s — nothing to audit\n", root)
		return 1
	}
	// The cost stated before anything starts (issue #522's own shape): a
	// whole-crate run is the expensive one, measured elsewhere at 180 min of
	// baseline builds across 44 runs even with a warm shared target dir.
	fmt.Fprintf(stdout, "aphrollo: auditing %s in full — this measures every mutable line, not a diff, and is the expensive shape (baseline builds alone measured 180 min across 44 runs with a warm target dir). Interrupt anytime: nothing already written to mutants.out is lost.\n", scope)

	release := acquireMutantsRunLock("mutants audit for "+root, root)
	defer release()

	var report AuditReport
	var err error
	if isRust {
		report, err = mutantsAuditRustFn(root, scope, stdout, stderr)
	} else {
		report, err = mutantsAuditGoFn(root, scope, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants audit: %v\n", err)
		return 1
	}
	renderAuditReport(stdout, report)
	return 0
}

// renderAuditReport prints ranked survivors and the totals beside them — read
// by a person deciding where to write a test, never parsed by a gate.
//
// Ranking happens here, not only inside outcomesToReport, so it is a
// property of the OUTPUT this verb prints regardless of which driver
// produced the report — a future driver that forgets to sort its own
// outcomes still renders a ranked list.
func renderAuditReport(out io.Writer, r AuditReport) {
	sortAuditSurvivors(r.Survivors)
	fmt.Fprintf(out, "\naphrollo: %s — %d mutant(s), %d caught, %d survivor(s)\n", r.Scope, r.Total, r.Caught, len(r.Survivors))
	for _, s := range r.Survivors {
		fmt.Fprintln(out, s.String())
	}
}

// sortAuditSurvivors ranks by file, then line, then column, then mutation —
// the same key sortOutcomes uses, so two runs of the same tree print the
// survivors in the same order.
func sortAuditSurvivors(s []AuditSurvivor) {
	sort.Slice(s, func(i, j int) bool {
		a, b := s[i], s[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Mutation < b.Mutation
	})
}
