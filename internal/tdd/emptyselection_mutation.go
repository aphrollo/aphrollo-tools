package tdd

import (
	"strings"
)

// widenCargoRunner drops a narrowed cargo run's within-package narrowing and
// keeps its package scope, reporting false when there was nothing to drop
// (the run is already as wide as this crate goes) or when the runner is a
// build-only target — an example or a bench selects zero tests BY
// CONSTRUCTION, and widening it into the package's whole suite would cost
// minutes to answer a question nobody asked.
//
// A range over the arguments rather than an index the body advances: a scope
// flag's value is kept by marking the flag and taking the NEXT word when the
// walk reaches it, so no edit to the bookkeeping can send the walk backwards
// into an endless loop, and a trailing flag with no value has nothing to
// index past. A narrowing flag's own value needs no mark — it is a bare word
// after a flag, which is the positional filter and dropped anyway.
func widenCargoRunner(r Runner) (Runner, bool) {
	if r.Cmd != "cargo" || buildOnlyRunner(r) {
		return Runner{}, false
	}
	out := make([]string, 0, len(r.Args))
	dropped, seenFlag, valueOfPrev := false, false, false
	for _, a := range r.Args {
		if valueOfPrev {
			out = append(out, a)
			valueOfPrev = false
			continue
		}
		name := flagName(a)
		switch {
		case cargoNarrowingBareFlags[name], cargoNarrowingValueFlags[name]:
			dropped, seenFlag = true, true
		case cargoScopeValueFlags[name]:
			out = append(out, a)
			valueOfPrev = !strings.Contains(a, "=")
			seenFlag = true
		case strings.HasPrefix(a, "-"):
			out = append(out, a)
			seenFlag = true
		case seenFlag:
			// A bare word after a flag is cargo's positional name filter,
			// or a narrowing flag's value; the verb words (`test`,
			// `nextest run`) come first and are kept.
			dropped = true
		default:
			out = append(out, a)
		}
	}
	if !dropped {
		return Runner{}, false
	}
	return Runner{Cmd: r.Cmd, Args: out, Dir: r.Dir}, true
}
