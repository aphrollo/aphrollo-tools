package mutation

import (
	"regexp"
	"strings"
)

// A proof names the test its mutation must fail, so the first run it makes
// is scoped to THAT test rather than to the mutated file's module. A module
// selection runs every test under the module, and a faster one that the same
// mutation also breaks fails first: with fail-fast on, the wanted test never
// runs and the proof reads WRONG FAILURE for a mutant the named test does
// kill. The scoped run keeps the narrowed target (cheap to build), replaces
// the name filter with the wanted test, and turns fail-fast off; an empty
// scoped run climbs the widening ladder in widenSurvivorSelection.

// cargoTestNameRe and goTestNameRe are the --want-fail spellings a filter
// can carry verbatim. Anything else (a space, a slash, a parenthesis) could
// break the filter expression, so such a name is not scoped and the run
// keeps the narrowed selection it had.
var (
	cargoTestNameRe = regexp.MustCompile(`^[A-Za-z0-9_:]+$`)
	goTestNameRe    = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

// scopeToWantedTest narrows a proof's runner to the named test, in the
// runner's own dialect. A build-only cargo runner executes no test and is
// left as it is.
func scopeToWantedTest(r Runner, want string) Runner {
	switch r.Cmd {
	case "cargo":
		if buildOnlyRunner(r) {
			return r
		}
		base := r
		if dropped, ok := dropCargoNameFilter(r); ok {
			base = dropped
		}
		args := append(append([]string{}, base.Args...), "--no-fail-fast")
		if cargoTestNameRe.MatchString(want) {
			args = append(args, wantedTestFilterArgs(base, want)...)
		}
		return Runner{Cmd: r.Cmd, Args: args, Dir: r.Dir}
	case "go":
		if !goTestNameRe.MatchString(want) || len(r.Args) == 0 {
			return r
		}
		args := append([]string{r.Args[0], "-run=^" + want + "$"}, r.Args[1:]...)
		return Runner{Cmd: r.Cmd, Args: args, Dir: r.Dir}
	}
	return r
}

// wantedTestFilterArgs selects one test by name: nextest matches the whole
// name or its last `::` segments, the same suffix rule matchWantFail reads
// the verdict with; libtest's positional filter is a substring.
func wantedTestFilterArgs(r Runner, want string) []string {
	if strings.HasPrefix(strings.Join(cargoRunArgs(r), " "), "nextest") {
		return []string{"-E", "test(/(^|::)" + want + "$/)"}
	}
	return []string{want}
}

// dropCargoTarget removes a cargo run's target selection (`--lib`, `--test
// <t>`, …) and keeps its name filter and package scope: the rung between a
// scoped target that selected nothing and the whole package. false when the
// run named no target.
func dropCargoTarget(r Runner) (Runner, bool) {
	out := make([]string, 0, len(r.Args))
	dropped, skipValue := false, false
	for _, a := range r.Args {
		if skipValue {
			skipValue = false
			continue
		}
		name := flagName(a)
		switch {
		case cargoNarrowingBareFlags[name]:
			dropped = true
		case cargoTargetValueFlags[name]:
			dropped = true
			skipValue = !strings.Contains(a, "=")
		default:
			out = append(out, a)
		}
	}
	if !dropped {
		return Runner{}, false
	}
	return Runner{Cmd: r.Cmd, Args: out, Dir: r.Dir}, true
}

// goRunFiltered reports whether a go runner carries a `-run` name filter:
// dropping it is a widening even when no other package reaches the file,
// because the package's other tests have not run.
func goRunFiltered(r Runner) bool {
	for _, a := range r.Args {
		if strings.HasPrefix(a, "-run=") {
			return true
		}
	}
	return false
}
