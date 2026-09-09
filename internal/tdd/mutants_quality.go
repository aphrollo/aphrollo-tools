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
// The first is answered by refusing a merge whose mutants timed out (a timeout is an
// unmeasured mutant, not a result) and by capping concurrency hard. The second
// by letting the repo name the switches its mutation run must set.

// MutantsJobsCap is how many mutants may be measured at once, and why. A
// mutation run shares the box with the editors it exists to serve, so the cap
// is deliberately mean: one job per six cores, one per six gigabytes, never
// more than two whatever the machine is, never less than one.
//
// The memory term is what is FREE, not what is installed, whenever a free
// reading can be taken — mutants_freemem.go has the incident it comes from.
// availGB of 0 is an unreadable box and falls back to total RAM.
func MutantsJobsCap(cores, ramGB, availGB int) (int, string) {
	byCores := cores / 3
	jobs, why := 8, "cap 8"
	if byCores < jobs {
		jobs, why = byCores, "cores"
	}
	// Memory that could not be READ is not memory that is absent. Folding a
	// zero into the minimum pinned every non-Linux unix to one job — a wrong
	// number derived from a missing one — so an unknown reading simply does
	// not constrain, and the cores decide alone.
	byMem, memTerm, mem, known := mutantsMemoryTerm(ramGB, availGB, 8, "8")
	if known && byMem < jobs {
		jobs, why = byMem, memTerm
	}
	if jobs < 1 {
		jobs = 1
	}
	return jobs, fmt.Sprintf("min(cores %d/3=%d, %s, cap 8) — %s", cores, byCores, mem, why)
}

// mutantsBoxShapeFn is the box itself: how many cores it has, how much memory
// is installed, and how much of it is free at the moment of the call. ONE
// seam for all three numbers, because every derivation that divides the box
// between concurrent builds — the shard count here, the per-shard build width
// in mutants_buildjobs.go — has to be testable against a hypothetical box
// without acquiring one.
//
// The free reading is the one that MOVES, so a caller reads this seam once
// per run and carries the answer: two shards of one measurement deriving
// different widths from two instants would be one run that cannot explain
// itself.
var mutantsBoxShapeFn = func() (cores, ramGB, availGB int) {
	return runtime.NumCPU(), machineRAMGB(), machineAvailGB()
}

// setMutantsBoxForTest pins the box's shape for one test.
func setMutantsBoxForTest(cores, ramGB, availGB int) (restore func()) {
	prev := mutantsBoxShapeFn
	mutantsBoxShapeFn = func() (int, int, int) { return cores, ramGB, availGB }
	return func() { mutantsBoxShapeFn = prev }
}

// mutantsJobsForThisBox is the cap for the machine the job runs on. Memory is
// read where it can be; where it cannot, the core count decides alone — a
// wrong-way guess about RAM would raise the cap, and this cap only ever
// lowers.
func mutantsJobsForThisBox() (int, string) {
	return MutantsJobsCap(mutantsBoxShapeFn())
}

// mutantsJobsForThisBoxFn is that derivation as a seam. The Go runner's argv
// carries the number, so a test asserting the argv EXACTLY would otherwise be
// asserting how many cores and how much memory the machine running it has: it
// passed on a 24-core developer box and failed on every CI runner, which
// derive one. The box is the thing under test in MutantsJobsCap's own cases,
// and nowhere else.
var mutantsJobsForThisBoxFn = mutantsJobsForThisBox

// setMutantsJobsForTest pins the derived cap for one test.
func setMutantsJobsForTest(jobs int, why string) (restore func()) {
	prev := mutantsJobsForThisBoxFn
	mutantsJobsForThisBoxFn = func() (int, string) { return jobs, why }
	return func() { mutantsJobsForThisBoxFn = prev }
}
