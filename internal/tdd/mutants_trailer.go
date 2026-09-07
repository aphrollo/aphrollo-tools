package tdd

import (
	"fmt"
	"strings"
)

// mutantsRunRequested reports whether HEAD's own commit message carries a
// `Mutants: run` trailer (issue #521). It is the ONLY signal the detached
// post-commit path accepts to start a run: a time-based debounce was
// considered and rejected because it guesses wrong in both directions — a
// long pause mid-lane spawns a run that is superseded anyway, a fast final
// commit starts the run late — and with only two build slots on the box every
// speculative run displaces a real one. The author knows when a lane is
// ready; the hook can only infer it, so it stops inferring and reads what was
// asked instead.
//
// Read through git's OWN trailer parser (`%(trailers:...)`, the same
// machinery `git interpret-trailers` uses) rather than a text scan of the
// message body: that is what tells a genuine trailer block ("Title\n\nMutants:
// run") apart from the words appearing in ordinary prose, and matches the key
// case-insensitively the way every other git trailer is matched. The value is
// compared case-insensitively too, so "Mutants: RUN" counts.
//
// A non-nil error means "could not tell", never "no trailer" — this is a
// TRIGGER, not an ordinary query: a transient git failure silently read as
// "the author did not ask" would drop an explicitly requested run with
// nothing logged, in exactly the case the feature exists to serve. The
// caller (buildMutantsJob) keeps that distinct from an ordinary "no trailer"
// refusal by printing it as a non-routine one.
func mutantsRunRequested(root string) (bool, error) {
	out, err := git(root, "log", "-1", "--format=%(trailers:key=Mutants,valueonly,key_value_separator= )", "HEAD")
	if err != nil {
		return false, fmt.Errorf("git log HEAD failed: %w (%s)", err, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "run") {
			return true, nil
		}
	}
	return false, nil
}
