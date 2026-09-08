package tdd

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
	conclusive bool
	// vacuous: the run exited 0 having executed zero tests (#317), named per
	// package/target in vacuousPkgs.
	vacuous     bool
	vacuousPkgs []string
	// dur is the suite's own measured Duration, 0 for every path that
	// returned before a suite ran.
	dur time.Duration
	// cmd is the executed argv, "" when nothing ran.
	cmd string
}
