package tdd

import (
	"fmt"
	"runtime"
)

// What a mutation run reports is only worth as much as the conditions it ran
// under. Two measurements from one lane say why this file exists:
//
//	9 timeouts at 30 s — eight cold tree copies were compiling at once, so the
//	   suite could not finish inside cargo-mutants' default budget. Every one
//	   of those was a mutant nobody measured, filed beside the ones that were.
//	101 of 167 mutants missed — they lived in render-world code that only an
//	   env-gated GPU suite reaches, and the run set no switches, so the tests
//	   that would have caught them never ran.
//
// The first is answered by refusing a receipt with timeouts (a timeout is an
// unmeasured mutant, not a result) and by capping concurrency hard. The second
// by letting the repo name the switches its mutation run must set.

// MutantsJobsCap is how many mutants may be measured at once, and why. A
// mutation run shares the box with the editors it exists to serve, so the cap
// is deliberately mean: one job per six cores, one per six gigabytes, never
// more than two whatever the machine is, never less than one.
func MutantsJobsCap(cores, ramGB int) (int, string) {
	byCores, byRAM := cores/6, ramGB/6
	jobs, why := 2, "cap 2"
	if byCores < jobs {
		jobs, why = byCores, "cores"
	}
	if byRAM < jobs {
		jobs, why = byRAM, "ram"
	}
	if jobs < 1 {
		jobs = 1
	}
	return jobs, fmt.Sprintf("min(cores %d/6=%d, ram %dGB/6=%d, cap 2) — %s", cores, byCores, ramGB, byRAM, why)
}

// mutantsJobsForThisBox is the cap for the machine the job runs on. Memory is
// read where it can be; where it cannot, the core count decides alone — a
// wrong-way guess about RAM would raise the cap, and this cap only ever
// lowers.
func mutantsJobsForThisBox() (int, string) {
	return MutantsJobsCap(runtime.NumCPU(), machineRAMGB())
}

// cargoMutantsEnv reads the env switches a workspace declares its mutation run
// must set: `[workspace.metadata.aphrollo] mutants-env = ["NAME=1", …]`. Each
// one must also be in the repo's dev-instrument registry — a switch that gates
// a whole suite is exactly what that registry exists to name.
func cargoMutantsEnv(ws string) []string {
	return cargoAphrolloPackages(ws, "mutants-env")
}
