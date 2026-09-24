package precommit

import (
	"fmt"
	"os"
	"strings"
)

// The two quality checks, over the crates the commit actually touched. They
// sit at different points in the gate's cost order: fmt runs first of
// everything (milliseconds), clippy after the guard crates and before the
// touched crates' own suites. Deliberately different in how they opt in:
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
// timeout or a busy build slot — the same policy as the suite stages, since
// neither is a verdict about the code.

// qualityCheck selects which of the two checks a call runs: they sit at
// DIFFERENT points in the cost order (fmt first of everything, clippy after
// the guard crates), so one call cannot do both.
type qualityCheck int

const (
	qualityFmt qualityCheck = iota
	qualityClippy
)

// cargoQualityStage returns a blocking GateResult for the first crate that
// fails the selected check. ws is the cargo workspace root the commands run from, pkgs
// the packages the staged files belong to (never the always-run additions —
// those are not what this commit touched).
func cargoQualityStage(gateName, ws, root string, pkgs []string, run SuiteRunner, repoRoot string, which qualityCheck) GateResult {
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
		if which == qualityFmt {
			// fmt compiles nothing, so it never takes a build slot.
			fmtRunner := Runner{Cmd: "cargo", Args: []string{"fmt", "--check", "-p", pkg}, Dir: ws}
			if blocked := qualityVerdict(gateName, root, pkg, "fmt", fmtRunner, run(fmtRunner, root), true); blocked != nil {
				return *blocked
			}
			continue
		}
		if !clippyClean[pkg] {
			continue
		}
		clippyRunner := Runner{Cmd: "cargo", Args: []string{"clippy", "-p", pkg, "--tests", "--", "-D", "warnings"}, Dir: ws}
		// No budget floor (the trailing zero): clippy runs no suite, and
		// this check fails OPEN on a timeout anyway — a longer budget would
		// buy a verdict nobody is blocked on.
		res, waited, acquired := runCargoLocked(run, clippyRunner, root, precommitLockWait(), DefaultPrecommitTimeout, 0)
		logLockWait(gateName, root, clippyRunner, waited)
		if blocked := qualityVerdict(gateName, root, pkg, "clippy", clippyRunner, res, acquired); blocked != nil {
			return *blocked
		}
	}
	return GateResult{}
}

// qualityVerdict turns one check's result into a block, or nil to continue.
// A timeout and a check-error BLOCK via verdictFor, the same policy the
// mechanical stage holds; an unavailable build slot blocks here directly
// because it is not one of verdictFor's six outcomes.
func qualityVerdict(gateName, root, pkg, stage string, r Runner, res SuiteResult, acquired bool) *GateResult {
	switch {
	case !acquired:
		// A check that never ran has proven nothing, and the suite stage
		// rejects for exactly this reason: the two must agree.
		fmt.Fprintf(os.Stderr, "gate %s: %s -p %s in %s QUEUED-REJECTED (no build slot came free)\n", gateName, stage, pkg, root)
		AppendGateLog(gateName, root, cmdString(r), stage+"-queued-rejected", 0)
		return &GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate %s: could not run %s -p %s in %s: every build slot stayed busy for the whole wait, so nothing was checked and the commit is refused. Retry when the build finishes.",
			gateName, stage, pkg, root)}
	case res.TimedOut:
		got := verdictFor(gateName, stage, root, cmdString(r), stageOutcome{
			Kind:   outcomeTimeout,
			Result: res,
			Message: fmt.Sprintf(
				"gate %s: %s -p %s in %s did not finish, so nothing was checked and the commit is refused.",
				gateName, stage, pkg, root),
		})
		return &got
	case !res.Passed:
		fmt.Fprintf(os.Stderr, "gate %s: %s -p %s in %s → blocked\n", gateName, stage, pkg, root)
		AppendGateLog(gateName, root, cmdString(r), stage+"-blocked", res.Duration)
		return &GateResult{Blocked: true, Message: qualityRejectMessage(pkg, stage, r, res)}
	default:
		fmt.Fprintf(os.Stderr, "gate %s: %s -p %s in %s → clean\n", gateName, stage, pkg, root)
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

// firstDiagnostic picks the first compile error anywhere in the output
// (firstError: an error outranks a warning printed ahead of it, issue #791),
// else the first line that reads like a tool diagnostic (rustfmt's "Diff in
// <file>", clippy's "warning:"), falling back to the first non-empty line so
// a message is never empty.
func firstDiagnostic(output string) string {
	if first := firstError(output); first != "" {
		return first
	}
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
