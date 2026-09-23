package suite

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
// The zero value is outcomeUnset, deliberately NOT outcomePass: a
// stageOutcome literal that omits kind is a defect in the CALLER (a stage
// that forgot to classify its own result), never a stage that genuinely
// passed, and verdictFor's default branch treats it exactly like a kind it
// does not recognize — both block, loudly, rather than falling through to a
// silent, unlogged pass (#361).
type stageOutcomeKind int

const (
	outcomeUnset stageOutcomeKind = iota
	outcomePass
	outcomeFail
	outcomeTimeout
	outcomeSkipped
	outcomeRunnerMissing
	outcomeCheckError
	// outcomeVacuous is #317's "zero tests executed" outcome: the runner
	// exited 0, but the count it actually ran was zero — a TestMain that
	// skipped m.Run(), a gremlin walk over an empty tree, a --diff base that
	// excluded every mutant. Never a pass; always a block.
	outcomeVacuous
	// outcomeContention is a stage that could not even run because of BOX
	// CONTENTION — this gate's own lock stayed held for the whole wait, or
	// an external tool's own lock (golangci-lint's machine-wide flock)
	// reports the same — never a verdict about the code. It blocks, since
	// nothing was proven either way, but is logged and phrased as a retry,
	// never as a failure: the defect this exists to prevent is a "fix
	// before committing" message over a check that was never actually run.
	outcomeContention
)

// stageOutcome is what a stage hands verdictFor: which of the shapes this
// run took, the SuiteResult when one exists (its Duration matters for
// timeout and fail), the error a check-error carries, a short reason for a
// deliberate skipped/runner-missing stand-down, and the ready-to-show block
// message for the outcomes that block.
type stageOutcome struct {
	Kind    stageOutcomeKind
	Result  SuiteResult
	Err     error
	Reason  string
	Message string
}

// registeredStages lists every stage name a precommit/prepush check
// currently passes to verdictFor. TestVerdictFor_BlocksOnTimeoutForEvery-
// RegisteredStage and its check-error twin enumerate this list, so a stage
// name added here without also blocking on those two outcomes fails the
// table test immediately — verdictFor's mapping does not vary by stage, so
// keeping this list current is a documentation exercise, not a behavior one.
var registeredStages = []string{
	"vet", "lint", "fmt", "clippy", "check", "doctest", "mechanical", "docs", "ratchet-fixtures", "ratchet-check",
	"fail-first",
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
	switch o.Kind {
	case outcomePass:
		return GateResult{}
	case outcomeSkipped:
		line := fmt.Sprintf("[%s] gate %s: in %s → skipped (%s)", stage, gateName, root, o.Reason)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "skipped", o.Result.Duration)
		return GateResult{Message: line}
	case outcomeRunnerMissing:
		line := fmt.Sprintf("[%s] gate %s: in %s → skipped (%s)", stage, gateName, root, o.Reason)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "runner-missing", 0)
		return GateResult{Message: line}
	case outcomeTimeout:
		// A timeout is the one verdict where the code under test may be
		// entirely innocent (#526): sample the machine once, here on the
		// rejection path only, and carry it in both the console line and
		// the message a session actually reads, so "this suite got slower"
		// and "something unrelated ate the cores" no longer have to be
		// argued from the log alone.
		load := foreignLoadReport(os.Getpid())
		line := fmt.Sprintf("[%s] gate %s: %s in %s TIMEOUT after %.0fs REJECTED (nothing was tested)\n%s",
			stage, gateName, cmd, root, o.Result.Duration.Seconds(), load)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "timeout-rejected", o.Result.Duration)
		return GateResult{Blocked: true, Message: o.Message + "\n" + load}
	case outcomeCheckError:
		line := fmt.Sprintf("[%s] gate %s: in %s → REJECTED (%v)", stage, gateName, root, o.Err)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "check-error-rejected", 0)
		return GateResult{Blocked: true, Message: o.Message}
	case outcomeFail:
		fmt.Fprintf(os.Stderr, "[%s] gate %s: %s in %s → blocked\n", stage, gateName, cmd, root)
		logSuiteVerdict(gateName, root, cmd, stage+"-blocked", o.Result)
		return GateResult{Blocked: true, Message: o.Message}
	case outcomeVacuous:
		line := fmt.Sprintf("[%s] gate %s: %s in %s → REJECTED (0 tests executed; nothing was tested)",
			stage, gateName, cmd, root)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "vacuous-rejected", o.Result.Duration)
		return GateResult{Blocked: true, Message: o.Message}
	case outcomeContention:
		line := fmt.Sprintf("[%s] gate %s: %s in %s → REJECTED (box contention: %s)", stage, gateName, cmd, root, o.Reason)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "contention-rejected", o.Result.Duration)
		return GateResult{Blocked: true, Message: o.Message}
	default:
		// outcomeUnset (a stageOutcome literal that never set kind) and any
		// kind this switch does not recognize land here. Neither is a pass:
		// the first is a caller defect, the second is an outcome added to
		// the enum without teaching this function about it, and a stage
		// whose outcome the gate cannot classify is exactly the case where
		// continuing is unsafe.
		line := fmt.Sprintf("[%s] gate %s: %s in %s → REJECTED (unclassified stage outcome kind %d)",
			stage, gateName, cmd, root, o.Kind)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmd, "unclassified-outcome-rejected", 0)
		return GateResult{Blocked: true, Message: line}
	}
}
