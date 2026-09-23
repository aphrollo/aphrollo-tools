package mutation

import (
	"strings"
)

// goRanNoTests reports whether a passing Go run executed no test in any
// package (goRunRanATest). A run whose packages cannot be read is not read
// as empty.
func goRanNoTests(r Runner, res SuiteResult) bool {
	if r.Cmd != "go" || res.TimedOut || !res.Passed {
		return false
	}
	ran, known := goRunRanATest(res)
	return known && !ran
}

// cargoTargetValueFlags select a target by name and keep it on the lib rung.
var cargoTargetValueFlags = map[string]bool{"--test": true, "--bin": true}

// dropCargoNameFilter removes a cargo run's name filter and keeps its target
// selection and package scope, reporting false when the run carried no name
// filter at all.
//
// A range over the arguments rather than an index the body advances: a
// value flag's argument is consumed by marking it, so no edit to the
// bookkeeping can send the walk backwards into an endless loop. A filter
// flag's own value needs no mark — it is a bare word after a flag, which is
// the positional filter and dropped anyway.
func dropCargoNameFilter(r Runner) (Runner, bool) {
	out := make([]string, 0, len(r.Args))
	dropped, seenFlag, valueOfPrev := false, false, false
	for i, a := range r.Args {
		if valueOfPrev {
			valueOfPrev = false
			continue
		}
		name := flagName(a)
		inline := strings.Contains(a, "=")
		switch {
		case cargoNameFilterFlags[name]:
			dropped, seenFlag = true, true
		case cargoScopeValueFlags[name], cargoTargetValueFlags[name]:
			out = append(out, a)
			if !inline && i+1 < len(r.Args) {
				out = append(out, r.Args[i+1])
				valueOfPrev = true
			}
			seenFlag = true
		case strings.HasPrefix(a, "-"):
			out = append(out, a)
			seenFlag = true
		case seenFlag:
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

// cargoNameFilterFlags are the name filters within a cargo target: nextest's
// filter expression in both spellings. Plain cargo test's positional
// substring is the other one, recognised by position in dropCargoNameFilter.
var cargoNameFilterFlags = map[string]bool{"-E": true, "--filter-expr": true}
