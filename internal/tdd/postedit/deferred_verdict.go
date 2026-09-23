package postedit

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

// infraFailureLine reports a phase that DID spawn and finish, but whose own
// setup failed before its command ever ran (RunPhase's PhaseOutcome.SetupFailed)
// — no build slot came free, or it could not open its log file.
// Same InfraFailed family as spawnFailedLine, named by the wrapper's own
// first log line where there is one, so a capacity refusal names the
// resource it waited for rather than reading as a bare assertion failure.
//
// It names the JOB first (issue #583). The wrapper's log line names whoever
// held the slot, and that holder is routinely a build in another repo
// entirely: `infra-failed in <root> (no build slot came free ("cargo nextest
// run -p engine_audio" in borld))` carried no word about the run it was a
// verdict on, so it read as a verdict about somebody else's work. The
// blocker still has to be named — it is the only route to "the box was
// full, wait or retry" — but after the run it blocked, never instead of it.
func infraFailureLine(root string, j DeferredJob, res SuiteResult) string {
	reason := strings.TrimSpace(firstLine(res.Output))
	if reason == "" {
		reason = "the phase's own setup failed before its command started"
	}
	if subject := deferredRunSubject(j); subject != "" {
		return fmt.Sprintf("gate: → %s in %s (%s never ran: %s — the code was NOT tested)",
			InfraFailed, root, subject, reason)
	}
	return fmt.Sprintf("gate: → %s in %s (%s — the code was NOT tested)", InfraFailed, root, reason)
}

// deferredRunSubject names the run a verdict is about, in the possessive:
// `this session's run phase ("cargo nextest run -p sim")`. The possessive is
// the load-bearing part — every other name in an infra-failure line belongs
// to the build that blocked it, and a reader needs one clause that is
// unambiguously about their own edit. Empty for a record with no runner (one
// written before that field existed, or a setup that failed before a runner
// was chosen), which infraFailureLine reads as "nothing to attribute this
// to" rather than printing an empty subject.
func deferredRunSubject(j DeferredJob) string {
	cmd := strings.Join(j.Runner, " ")
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	if j.Phase == "" {
		return fmt.Sprintf("this session's %q", cmd)
	}
	return fmt.Sprintf("this session's %s phase (%q)", j.Phase, cmd)
}

// deferredOwnerNote attributes a harvested verdict to the session that
// started the run. Every hook harvest is keyed session+project
// (deferredJobPath), so a hook only ever reads its own work and needs no
// such note; `gate status --wait` is the one reader that finds a job by
// PROJECT alone, across sessions, and printed the resulting verdict verbatim
// — a line about somebody else's edit with nothing in it to say so (issue
// #583). Empty session id -> no note: there is nothing to attribute to.
func deferredOwnerNote(session string) string {
	session = strings.TrimSpace(session)
	if session == "" {
		return ""
	}
	return fmt.Sprintf(" [that run was started by session %s]", session)
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

// staleVerdictLine reports a finished result whose tree has moved on since
// it started: the real verdict, named by its tree and command, labelled as
// measured on an earlier tree state. A stale green must never read as a
// current green, and a stale red is still worth seeing — it is usually a
// real break the session made. Nothing is stamped or logged as a verdict:
// it describes code that is no longer on disk.
func staleVerdictLine(j DeferredJob, out PhaseOutcome) string {
	return fmt.Sprintf("gate: deferred %s in %s → %s — measured on an earlier tree state; not a verdict on the current code: the current code was NOT tested",
		strings.Join(j.Runner, " "), j.Project, staleVerdictLabel(j, out))
}

// staleVerdictLabel is what a stale result actually said, classified the way
// a current one would be.
func staleVerdictLabel(j DeferredJob, out PhaseOutcome) string {
	res := phaseSuiteResult(j, out)
	if out.SetupFailed {
		return InfraFailed
	}
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	if j.Phase == "build" && res.Passed {
		return "build ok, no test ran"
	}
	outcome := ClassifyOutcome(res.Passed, res.Output, nil)
	if !outcome.IsRed() {
		return greenLabel(outcome, res.Output, res.Duration)
	}
	if first := firstFailingName(res.Output); first != "" {
		return fmt.Sprintf("%s (first failure: %s)", outcome, first)
	}
	return string(outcome)
}
