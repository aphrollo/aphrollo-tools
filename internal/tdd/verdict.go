package tdd

import (
	"fmt"
	"os"
)

// stageOutcomeKind is the closed set of ways any precommit/prepush check can
// end, independent of which ecosystem (cargo, go, ratchet, docs) or which
// SuiteRunner produced it. check-error covers a check whose OWN machinery
// could not answer — an unreadable file, a law this binary's schema cannot
// parse, ratchet.RunFixtures erroring before it ran a single fixture — the
// same "nothing was proven" shape a timeout is, just from a different cause.
//
// This is the seam #317's "zero tests executed" outcome slots into: one more
// const here, one more case in verdictFor's switch, no existing case's
// behavior touched.
type stageOutcomeKind int

const (
	outcomePass stageOutcomeKind = iota
	outcomeFail
	outcomeTimeout
	outcomeSkipped
	outcomeRunnerMissing
	outcomeCheckError
)

// stageOutcome is what a stage hands verdictFor: which of the shapes this
// run took, the SuiteResult when one exists (its Duration matters for
// timeout and fail), the error a check-error carries, a short reason for a
// deliberate skipped/runner-missing stand-down, and the ready-to-show block
// message for the outcomes that block.
type stageOutcome struct {
	kind    stageOutcomeKind
	result  SuiteResult
	err     error
	reason  string
	message string
}

// registeredStages lists every stage name a precommit/prepush check
// currently passes to verdictFor. TestVerdictFor_BlocksOnTimeoutForEvery-
// RegisteredStage and its check-error twin enumerate this list, so a stage
// name added here without also blocking on those two outcomes fails the
// table test immediately — verdictFor's mapping does not vary by stage, so
// keeping this list current is a documentation exercise, not a behavior one.
var registeredStages = []string{
	"vet", "lint", "fmt", "clippy", "check", "doctest", "mechanical", "docs", "ratchet-fixtures", "ratchet-check",
}

// verdictFor is the one place a stage's raw outcome becomes a GateResult.
// Every non-pass outcome is announced on stderr and logged to gate.log with
// a token stable across every stage that reports it ("timeout-rejected",
// "check-error-rejected", …), so `gate stats` can count timeouts and
// check-errors across the whole gate without knowing which stage produced
// them. Policy: fail, timeout and check-error BLOCK; skipped and
// runner-missing are deliberate, logged stand-downs that let the commit
// through; pass blocks nothing and logs nothing (the caller's own success
// line already carries the count/duration detail this function does not
// have).
func verdictFor(gateName, stage, root, cmd string, o stageOutcome) GateResult {
	switch o.kind {
	case outcomePass:
		return GateResult{}
	case outcomeSkipped:
		line := fmt.Sprintf("gate %s: %s in %s → skipped (%s)", gateName, stage, root, o.reason)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmd, "skipped", o.result.Duration)
		return GateResult{Message: line}
	case outcomeRunnerMissing:
		line := fmt.Sprintf("gate %s: %s in %s → skipped (%s)", gateName, stage, root, o.reason)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmd, "runner-missing", 0)
		return GateResult{Message: line}
	case outcomeTimeout:
		line := fmt.Sprintf("gate %s: %s %s in %s TIMEOUT after %.0fs REJECTED (nothing was tested)",
			gateName, stage, cmd, root, o.result.Duration.Seconds())
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmd, "timeout-rejected", o.result.Duration)
		return GateResult{Blocked: true, Message: o.message}
	case outcomeCheckError:
		line := fmt.Sprintf("gate %s: %s in %s → REJECTED (%v)", gateName, stage, root, o.err)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmd, "check-error-rejected", 0)
		return GateResult{Blocked: true, Message: o.message}
	case outcomeFail:
		fmt.Fprintf(os.Stderr, "gate %s: %s %s in %s → blocked\n", gateName, stage, cmd, root)
		appendGateLog(gateName, root, cmd, stage+"-blocked", o.result.Duration)
		return GateResult{Blocked: true, Message: o.message}
	default:
		return GateResult{}
	}
}
