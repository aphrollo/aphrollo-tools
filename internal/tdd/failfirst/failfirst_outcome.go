package failfirst

import "time"

// failFirstOutcome is everything one fail-first proof run reports back to the
// stage that printed it. It exists because the stage line has to name the argv
// the proof ACTUALLY executed: it used to print the profile's detected command
// (`go test ./...`) whatever the run was narrowed to, which is what misread a
// day of gate.log as full-suite proofs (#567).
type failFirstOutcome struct {
	// violated: the staged tests PASSED without the staged source, so they
	// never went RED. conclusive: the proof reached a verdict at all —
	// false means the caller must not read violated either way.
	violated   bool
	Conclusive bool
	// vacuous: the run exited 0 having executed zero tests (#317), named per
	// package/target in vacuousPkgs.
	vacuous     bool
	vacuousPkgs []string
	// skipped: the run exited 0 having executed its selected tests and every
	// one of them SKIPPED itself (#656), named per package/target in
	// skippedPkgs. Distinct from vacuous — there the filter selected nothing,
	// here the tests were selected and declined at runtime — and distinct
	// from a pass, which is the whole point: neither says anything about
	// HEAD, and only one of them used to be admitted as a verdict.
	skipped     bool
	skippedPkgs []string
	// notReached: the run failed without reaching the staged tests — the
	// tool never started, or its config did not load (#898). Neither a red
	// proof nor a violation.
	notReached bool
	// res is the proof run's own result, kept so its output is retained
	// for `aphrollo gate output`; zero when nothing ran.
	res SuiteResult
	// runner is the proof's resolved Runner, kept so the violation message
	// can say which skips this gate can and cannot read.
	runner Runner
	// dur is the suite's own measured Duration, 0 for every path that
	// returned before a suite ran.
	dur time.Duration
	// cmd is the executed argv, "" when nothing ran.
	cmd string
	// standDown names WHY an inconclusive proof was inconclusive, for the
	// two causes the stage refuses on rather than letting the commit land.
	standDown failFirstStandDown
	// waited is how long the proof queued for a build slot before giving
	// up, meaningful only for failFirstNoBuildSlot.
	waited time.Duration
}

// failFirstStandDown distinguishes the two ways a fail-first proof ends having
// measured nothing (#561). They read identically in a log and need different
// remedies: a run killed at its budget says the proof itself (or the box) is
// too slow, while a proof that never got a build slot says nothing about the
// tests at all — it queued behind another lane and gave up. Both used to
// collapse into "inconclusive (fail-open)" and let the commit land unproven,
// observed three times on 2026-09-07 with four lanes sharing two slots.
type failFirstStandDown int

const (
	// failFirstProofRan is the zero value: no stand-down, so the outcome's
	// own conclusive/vacuous fields carry the verdict.
	failFirstProofRan failFirstStandDown = iota
	failFirstNoBuildSlot
	failFirstOverBudget
)
