package tdd

import "fmt"

// Issue #691. A src/ mutation is narrowed to `--lib` plus the file's module
// filter, and `--lib` never builds or runs the crate's integration binaries.
// When the only test that kills the mutant lives under tests/, that selection
// comes back green and the proof called it a SURVIVOR — a confident wrong
// answer in the direction that BLOCKS correct work, since the pre-merge gate
// refuses an unaccepted survivor by name, and a silent one: nothing in the
// output said the run could not have reached the killing test.
//
// The rule this file enforces is the same one NoTestsSelected enforces one
// branch over: a verdict may only claim what the run it was read from could
// have observed. "Nothing in this selection killed it" is not "nothing kills
// it".
//
// Two phases rather than simply dropping `--lib`, because the two branches
// are not equally likely and not equally expensive. A KILL settles on the
// cheap lib-only run and stops there; only a green — the rare branch — pays
// for building and running the integration binaries, and it pays exactly
// where the one-phase answer would have been wrong. The trap of a two-phase
// verdict is recording the wrong phase, so the widened runner and its result
// REPLACE the narrow pair for everything downstream: the verdict printed, the
// tests read out of it, and the run retained for `aphrollo gate output`.
//
// Only the diff-scoped measurement (`aphrollo gate mutants run`) is exempt,
// and by construction rather than by luck: cargo-mutants runs the package's
// whole nextest suite for every mutant, with no target narrowing to widen
// (MutantsArgv, pinned by its own test).

// widenSurvivorSelection re-runs a proof's green run with the within-package
// narrowing dropped, and hands back the pair the caller must judge on.
//
// It fires only where a SURVIVOR would otherwise be claimed. A red run has
// already observed something and is judged on its own name; a timeout says
// nothing about selection; a run that selected zero tests has its own
// refusal, which is already honest about having tested nothing. widened is
// false when there was no narrowing left to drop — the selection was already
// as wide as the crate goes, so the survivor claim covers every test that
// could have killed the mutant — or when the runner is not a cargo one, whose
// narrowing this cannot reason about.
func widenSurvivorSelection(run SuiteRunner, narrow Runner, root string, res SuiteResult) (Runner, SuiteResult, bool) {
	if res.TimedOut || !res.Passed || selectedZeroTests(narrow, res) {
		return narrow, res, false
	}
	wide, ok := widenCargoRunner(narrow)
	if !ok {
		return narrow, res, false
	}
	return wide, run(wide, root), true
}

// widenedProveNote is what a verdict reached through both phases says about
// the first one. A reader auditing a survivor has to be able to tell which
// selection produced it, and a reader auditing a kill has to be able to tell
// why the proof ran twice.
func widenedProveNote(narrow Runner, widened bool) string {
	if !widened {
		return ""
	}
	return fmt.Sprintf(" Widened first: %s stayed green, and a target-narrowed selection cannot run a "+
		"killing test outside the lib, so this verdict is the wider run's.", cmdString(narrow))
}
