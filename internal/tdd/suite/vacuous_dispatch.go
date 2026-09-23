package suite

// vacuousNames is the one dispatch point that runSuiteStage (mechrun.go) and
// failFirstViolatedAt (precommit_failfirst.go) both call: given a PASSED,
// non-timed-out run, it returns the names to report as having executed zero
// tests, or nil when the run genuinely proved something. Only Go's check can
// itself fail — vacuousGoPackages reads a structured `go test -json` stream
// that can end mid-decode; cargo/pytest/vitest read plain summary text, which
// has no equivalent partial-read failure mode, so their branches never
// return an error.
func vacuousNames(runner Runner, res SuiteResult) ([]string, error) {
	switch {
	case runner.Cmd == "go":
		return vacuousGoPackages(res.GoTestJSON)
	case runner.Cmd == "cargo":
		return cargoVacuousTargets(res.Output), nil
	case runner.Cmd == "pytest":
		if pytestVacuous(res.Output) {
			return []string{"pytest"}, nil
		}
		return nil, nil
	case runner.Cmd == "npx" && len(runner.Args) > 0 && runner.Args[0] == "vitest":
		if vitestVacuous(res.Output) {
			return []string{"vitest"}, nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}
