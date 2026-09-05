package fixture

// GateResult stands in for tdd.GateResult — see the hit fixture's comment.
// Exported names here (never used outside this fixture) sidestep the
// unused-symbol lint the gate itself runs on any touched Go package.
type GateResult struct {
	Blocked bool
	Message string
}

// SuiteResult stands in for tdd.SuiteResult.
type SuiteResult struct{ TimedOut bool }

// StageOutcome stands in for tdd.stageOutcome.
type StageOutcome struct{ Kind int }

// OutcomePass and OutcomeTimeout stand in for tdd's outcome kinds.
const (
	OutcomePass = iota
	OutcomeTimeout
)

// VerdictFor stands in for tdd.verdictFor.
func VerdictFor(kind int) GateResult { return GateResult{Blocked: kind == OutcomeTimeout} }

// ExampleStage routes every outcome through VerdictFor, the one place that
// owns the pass/fail/timeout/skipped/runner-missing/check-error mapping.
func ExampleStage(res SuiteResult) GateResult {
	if res.TimedOut {
		return VerdictFor(OutcomeTimeout)
	}
	return VerdictFor(OutcomePass)
}
