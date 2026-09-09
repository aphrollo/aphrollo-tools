package tdd

import (
	"fmt"
	"strings"
	"time"
)

// The two ways a deferred phase used to end without telling anyone anything
// (issue #571: 29 deferred, 3 green in one evening).
//
//  1. The job outlived the deferral ceiling. It was killed, its record
//     dropped, "deferred-abandoned" written to the gate log — and the hook
//     returned the FRESH run's BUILDING line, so the session read "in
//     progress" where the truth was "the previous run died without ever
//     testing your code". deferredAbandonedLine and joinDeferredAdvisory are
//     what make that verdict arrive at the session, in the one line a hook
//     gets, without hiding what is running now.
//  2. A `go test` phase inherited go's DEFAULT 10-minute timeout, which lands
//     on the same 600s the ceiling abandons at. Measured on this box: two
//     jobs logged "deferred-abandoned 600.4s" and "601.8s" — go's own panic
//     dump and the gate's kill racing each other, one clock too many.
//     withDeferredGoTimeout gives the phase a timeout ABOVE the ceiling, so
//     the ceiling is the only clock that ends a deferred run and the verdict
//     is always the gate's own (an honest inconclusive) rather than a
//     coin-flip between a lost result and a panic dump that would classify as
//     a red the code never earned.

// DeferredAbandoned is the verdict for a detached phase that outlived the
// deferral ceiling with no result. Inconclusive family, alongside
// TIMEOUT/SKIPPED/QUEUED-SKIPPED and InfraFailed — the code was NOT tested —
// and the exact token the gate log has always carried for it, so a reader of
// the log and a reader of the hook line see one name for one fact.
const DeferredAbandoned = "deferred-abandoned"

// deferredAbandonedLine is what a session is told about a job that ran out of
// ceiling. It names the phase, the project and how long the job lived, so
// "killed after 601s" reads as a suite that could not fit, distinct from
// "killed after 3s", which would be a clock or ceiling problem rather than a
// slow suite.
func deferredAbandonedLine(root, phase string, elapsed time.Duration) string {
	return fmt.Sprintf("gate: → %s (the deferred %s phase in %s was killed after %.0fs with no result — that edit's code was NOT tested)",
		DeferredAbandoned, phase, root, elapsed.Seconds())
}

// joinDeferredAdvisory folds a carried-over verdict (an abandonment the
// harvest just reported) and this edit's own line into the ONE line a hook
// prints, without stacking a second "gate: " prefix. Both facts have to
// travel: dropping the carried one is how an abandonment became invisible,
// and dropping this edit's own would leave the session unable to see what is
// running now.
func joinDeferredAdvisory(carried, line string) string {
	if carried == "" {
		return line
	}
	if line == "" {
		return carried
	}
	return carried + "; then " + strings.TrimPrefix(line, "gate: ")
}

// deferredGoTimeoutSlack is how far a deferred `go test`'s own -timeout sits
// ABOVE the deferral ceiling. It only has to be wide enough that the two
// clocks can never be confused for each other under load — the gate's kill is
// meant to be the one that fires, and go's is the backstop for a run the gate
// somehow never gets back to (a session that ended, a hook that never ran
// again), which is exactly the process the 24h sweep would otherwise leave
// compiling.
const deferredGoTimeoutSlack = 5 * time.Minute

// deferredGoTimeout is the value that phase names. Derived from the ceiling,
// never a copied constant: raising APHROLLO_DEFERRED_MAX_SECS must move this
// with it, or the collision this exists to remove comes straight back.
func deferredGoTimeout() time.Duration { return deferredMax() + deferredGoTimeoutSlack }

// withDeferredGoTimeout inserts that -timeout into a `go test` argv, right
// after "test". A no-op for anything that is not `go test`, and for a caller
// that already named a -timeout of its own (in either spelling `go test`
// accepts): doubling the flag is a go error, and a caller that tuned the
// value meant it.
func withDeferredGoTimeout(argv []string) []string {
	if len(argv) == 0 || !isGoTestInvocation(argv[0], argv[1:]) {
		return argv
	}
	for _, a := range argv {
		if a == "-timeout" || strings.HasPrefix(a, "-timeout=") {
			return argv
		}
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[0], argv[1], "-timeout="+deferredGoTimeout().String())
	return append(out, argv[2:]...)
}
