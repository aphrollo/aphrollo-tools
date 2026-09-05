package fixture

// GateResult stands in for tdd.GateResult: the law scans TEXT, not types, so
// a fixture never needs the real package — see suppression_reason's and
// dev_instrument_registry's fixtures for the same standalone convention.
// Exported names here (never used outside this fixture) sidestep the
// unused-symbol lint the gate itself runs on any touched Go package.
type GateResult struct {
	Blocked bool
	Message string
}

// SuiteResult stands in for tdd.SuiteResult.
type SuiteResult struct{ TimedOut bool }

// ExampleStage hand-maps a timeout to a GateResult instead of routing it
// through verdictFor — the exact shape #288/#287/#313 each found.
func ExampleStage(res SuiteResult) GateResult {
	if res.TimedOut {
		return GateResult{}
	}
	return GateResult{Message: "checked"}
}
