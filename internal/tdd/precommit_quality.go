package tdd

import (
	"fmt"
	"os"
	"strings"
)

// The quality stage runs AFTER the mechanical suite has passed, over the
// crates the commit actually touched. Two checks, deliberately different in
// how they opt in:
//
//   - `cargo fmt --check` runs for every touched crate. Formatting is
//     mechanical, has one right answer, and unformatted code lands as noise
//     in the NEXT person's diff.
//   - `cargo clippy -D warnings` runs only for crates the workspace declared
//     `clippy-clean`. In a large tree most crates carry warnings, so gating
//     all of them is a gate nobody can use; a crate that reached zero is
//     held there.
//
// Both are per-crate so a rejection can name one, and both fail OPEN on a
// timeout or a busy build slot — the same policy as the mechanical stage,
// since neither is a verdict about the code.

// cargoQualityStage returns a blocking GateResult for the first crate that
// fails a check. ws is the cargo workspace root the commands run from, pkgs
// the packages the staged files belong to (never the always-run additions —
// those are not what this commit touched).
func cargoQualityStage(gateName, ws, root string, pkgs []string, run SuiteRunner, repoRoot string) GateResult {
	if ws == "" || len(pkgs) == 0 {
		return GateResult{}
	}
	clippyClean := map[string]bool{}
	for _, p := range cargoClippyCleanPackages(ws) {
		clippyClean[p] = true
	}
	// Same target dir as the mechanical stage: a quality pass that compiled
	// into a different one would cold-build the whole crate to say nothing
	// new.
	restore := pinMechCargoTarget(Runner{Cmd: "cargo"}, repoRoot)
	defer restore()

	for _, pkg := range pkgs {
		// fmt compiles nothing, so it never takes a build slot.
		fmtRunner := Runner{Cmd: "cargo", Args: []string{"fmt", "--check", "-p", pkg}, Dir: ws}
		if blocked := qualityVerdict(gateName, root, pkg, "fmt", fmtRunner, run(fmtRunner, root), true); blocked != nil {
			return *blocked
		}
		if !clippyClean[pkg] {
			continue
		}
		clippyRunner := Runner{Cmd: "cargo", Args: []string{"clippy", "-p", pkg, "--tests", "--", "-D", "warnings"}, Dir: ws}
		res, _, acquired := runCargoLocked(run, clippyRunner, root, buildLockPrecommitDeadline, DefaultPrecommitTimeout)
		if blocked := qualityVerdict(gateName, root, pkg, "clippy", clippyRunner, res, acquired); blocked != nil {
			return *blocked
		}
	}
	return GateResult{}
}

// qualityVerdict turns one check's result into a block, or nil to continue.
// A timeout or an unavailable build slot is inconclusive, not a failure:
// it is announced on stderr and the commit proceeds, exactly as the
// mechanical stage does.
func qualityVerdict(gateName, root, pkg, stage string, r Runner, res SuiteResult, acquired bool) *GateResult {
	switch {
	case !acquired:
		fmt.Fprintf(os.Stderr, "tdd %s: %s -p %s in %s → QUEUED-SKIPPED (every build slot is busy) — not checked\n", gateName, stage, pkg, root)
		return nil
	case res.TimedOut:
		fmt.Fprintf(os.Stderr, "tdd %s: %s -p %s in %s → TIMEOUT (FAIL-OPEN — not checked)\n", gateName, stage, pkg, root)
		return nil
	case !res.Passed:
		fmt.Fprintf(os.Stderr, "tdd %s: %s -p %s in %s → blocked\n", gateName, stage, pkg, root)
		appendGateLog(gateName, root, cmdString(r), "quality-blocked", res.Duration)
		return &GateResult{Blocked: true, Message: qualityRejectMessage(pkg, stage, r, res)}
	default:
		fmt.Fprintf(os.Stderr, "tdd %s: %s -p %s in %s → clean\n", gateName, stage, pkg, root)
		return nil
	}
}

// qualityRejectMessage names the crate, the exact command to reproduce, and
// the FIRST diagnostic — the one being introduced by this commit, which is
// the whole point of gating a crate that was already clean.
func qualityRejectMessage(pkg, stage string, r Runner, res SuiteResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TDD quality: %s failed for crate %s — fix before committing.\n", stage, pkg)
	fmt.Fprintf(&b, "command: %s %s\n", r.Cmd, strings.Join(r.Args, " "))
	if first := firstDiagnostic(res.Output); first != "" {
		fmt.Fprintf(&b, "first diagnostic: %s\n", first)
	} else if res.Err != "" {
		fmt.Fprintf(&b, "runner error: %s\n", res.Err)
	}
	b.WriteString(tailSnippet(res.Output))
	return b.String()
}

// firstDiagnostic picks the first line that reads like a tool diagnostic
// (rustfmt's "Diff in <file>", clippy's "error:"/"warning:"), falling back
// to the first non-empty line so a message is never empty.
func firstDiagnostic(output string) string {
	fallback := ""
	for line := range strings.Lines(output) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if fallback == "" {
			fallback = trimmed
		}
		for _, prefix := range []string{"Diff in ", "error:", "error[", "warning:"} {
			if strings.HasPrefix(trimmed, prefix) {
				return trimmed
			}
		}
	}
	return fallback
}
