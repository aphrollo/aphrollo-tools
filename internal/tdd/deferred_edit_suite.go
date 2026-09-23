package tdd

// InfraFailed is the verdict for a phase the tooling never actually got to
// run: the OS could not spawn the detached runner, or RunPhase's own setup
// (no build slot came free, no log file) failed before the phase's command
// started. It reads distinctly from RedBogus (a real run whose TEST setup
// broke — a syntax or import error) on purpose: RedBogus sends a session to
// fix its test, which is the wrong move when nothing about the test was ever
// exercised (issues #350, #354). It joins TIMEOUT/SKIPPED/QUEUED-SKIPPED in
// the inconclusive family rather than the Outcome enum in classify.go,
// because no test output was ever classified — there is nothing for
// ClassifyOutcome to have seen.
const InfraFailed = "infra-failed"

func runnerDir(r Runner, root string) string {
	if r.Dir != "" {
		return r.Dir
	}
	return root
}
