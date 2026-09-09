package tdd

import "fmt"

// What a mutation run may have is not what the box has.
//
// Both budgets in this package — the shard count (mutants_quality.go) and the
// per-shard build width (mutants_buildjobs.go) — divided TOTAL RAM, which is
// a statement about the machine rather than about this run's share of it. On
// a box with other sessions compiling on it that number is fiction: 25 cargo
// and rustc processes were already running when one merge's measurement
// started, the derivation still answered `ram 63GB/8=7`, and six of the seven
// baselines died of the machine — LLVM out of memory, a failed 2.6 MB
// allocation, an ICE — after eighteen minutes of work that measured nothing.
//
// The bounding number is what is FREE right now: available commit on Windows
// (RAM plus pagefile minus everything already charged, which on a box with a
// pagefile pinned at 16 GB is the real ceiling and is neither RAM nor
// RAM+pagefile), MemAvailable on Linux. Both readers already sit beside
// machineRAMGB in the per-platform files.
//
// Three rules hold the change to the safe direction:
//
//	the term is the SMALLER of the two. An idle box reports more available
//	   commit than it has RAM, and a budget that took it at its word would
//	   start MORE work than today's code does on the strength of a number that
//	   can vanish while the run builds. This may only ever lower.
//	unknown is UNKNOWN. Zero means the reading could not be taken, and the
//	   run falls back to exactly today's arithmetic rather than to a guess.
//	the report says which term bound the answer and that it was measured, so
//	   a free-memory decision is told from a total-RAM one by reading the log.

// mutantsBudgetMemoryGB is the memory a run's budget may divide, and the word
// the report names it with: `free` when a measured reading is the binding
// one, `ram` otherwise. Zero is "nothing to divide", which every caller reads
// as "this term does not constrain".
func mutantsBudgetMemoryGB(ramGB, availGB int) (gb int, term string) {
	if availGB > 0 && (ramGB <= 0 || availGB < ramGB) {
		return availGB, "free"
	}
	return ramGB, "ram"
}

// mutantsMemoryTerm is that term with its arithmetic spelled out for the
// run's log — `ram 63GB/8=7`, or `free 24GB/8=3 (measured)` when the box was
// busy. per is what one unit of work is priced at and perLabel how the report
// writes it, because the two budgets divide by different things and each
// already prints its own divisor.
//
// known is false when neither reading could be taken; the text is then `ram
// unknown` and the caller lets the cores decide alone, which is the rule
// MutantsJobsCap has always followed for an unreadable RAM figure.
func mutantsMemoryTerm(ramGB, availGB, per int, perLabel string) (byMem int, term, text string, known bool) {
	gb, term := mutantsBudgetMemoryGB(ramGB, availGB)
	if gb <= 0 {
		return 0, "ram", "ram unknown", false
	}
	byMem = gb / per
	text = fmt.Sprintf("%s %dGB/%s=%d", term, gb, perLabel, byMem)
	if term == "free" {
		// Said out loud because it is the difference between a budget that
		// was measured on the box as it is and one assumed from its spec
		// sheet — the whole point of the reading.
		text += " (measured)"
	}
	return byMem, term, text, true
}
