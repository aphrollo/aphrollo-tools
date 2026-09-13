package tdd

// A mutation proof is the one stage whose run a human is most likely to need
// to read back: it reports a verdict about a tree that no longer exists on
// disk (the mutation is restored before the verdict is printed), so the run's
// own text is the ONLY evidence the verdict can be checked against. It was
// also the one stage that kept none of it — every other stage that runs a
// suite goes through logSuiteVerdict, this verb called the runner directly —
// so `aphrollo gate output` after a proof served whatever ran before it
// (clippy, in issue #666's report) and the proof's own run was gone.
//
// A verdict nobody can audit is worse than one that is occasionally wrong:
// the wrong one at least gets noticed.

// mutantsProveStage is what a retained proof run is filed under. Deliberately
// NOT one of suiteStages (bashsuite.go): a proof's run was made against a
// MUTATED tree that has since been restored, so it must never be read as a
// fresh verdict about the tree a session is standing in, and must never
// refuse a rerun.
const mutantsProveStage = "mutants-prove"

// proveVerdictWord is the logged/stored word for one proof exit code — the
// same vocabulary the printed verdict uses, so the record's header and the
// line the session read cannot describe different outcomes. The "mutant-"
// prefix keeps it clear of the mutation MEASUREMENT stage's "mutants-*"
// verdicts, which the stats table classifies by prefix.
func proveVerdictWord(code int) string {
	switch code {
	case ExitMutantsProveKilled:
		return "mutant-killed"
	case ExitMutantsProveSurvived:
		return "mutant-survived"
	case ExitMutantsProveWrongFailure:
		return "mutant-wrong-failure"
	case ExitMutantsProveUnreadable:
		return "mutant-unreadable"
	case ExitMutantsProveTimedOut:
		return "mutant-timed-out"
	case ExitMutantsProveNoTestsSelected:
		return "mutant-no-tests-selected"
	default:
		return "mutant-refused"
	}
}

// retainProveRun records the run a proof just made — its bytes under
// `aphrollo gate output`, its verdict in gate.log — and passes code straight
// back, so every settled branch records before it answers rather than after
// someone asks. A run that produced no output at all is not retained
// (retainSuiteOutput's own rule): it would overwrite a real record with
// nothing.
func retainProveRun(root string, runner Runner, res SuiteResult, code int) int {
	logSuiteVerdict(mutantsProveStage, root, cmdString(runner), proveVerdictWord(code), res)
	return code
}
