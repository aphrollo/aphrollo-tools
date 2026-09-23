package tdd

import "time"

// Split out of posttooluse.go (module_size, issue #430): executing ONE
// post-edit runner, and the trailing note an advisory may carry.

// runPostEditSuite executes one post-edit runner under the machine-wide
// build lock and returns either its result or the terminal advisory that
// ends the hook — already logged. Lifted out of postEditFile so the widened
// retry (resolveEmptySelection) inherits the identical lock, deadline and
// timeout-streak handling instead of a second copy of it that could drift.
// budget is what this run may spend: the whole post-edit timeout for the
// narrowed run, and only what that run left over for each rung above it.
func runPostEditSuite(run SuiteRunner, snap stateSnapshot, root, headSHA string, budget time.Duration) (SuiteResult, string) {
	// No budget floor here (the trailing zero): an edit hook's budget is not
	// a promise to finish, it is the foreground slice before the work goes
	// deferred and reports at the next hook, so a floor would only hold the
	// session at the keyboard for a verdict it is already arranged to get
	// later.
	res, _, acquired := runCargoLocked(run, snap.runner, root, buildLockPostEditDeadline, budget, 0)
	if !acquired {
		// Another cargo build already holds the machine-wide lock — the
		// suite never even started, so this is a DIFFERENT fact from a
		// timeout (which means "it ran and blew its budget") and must never
		// touch the timeout streak: lock contention has nothing to do with
		// whether THIS project's suite is slow.
		AppendGateLog("postedit", root, cmdString(snap.runner), "queued-skipped", 0)
		return res, queuedSkippedAdvisory(root, runnerTargetDir(snap.runner, root))
	}
	if res.TimedOut {
		// A killed run proves nothing about the code — the last REAL outcome
		// stays authoritative for the next delta (state is untouched beyond the
		// timeout streak), but the run itself must be reported: silence here
		// reads as "green" when it actually means "inconclusive, not tested".
		if snap.state != nil {
			snap.state.StampTimeout(root, headSHA)
			_ = snap.state.Save(snap.statePath)
		}
		AppendGateLog("postedit", root, cmdString(snap.runner), "timeout", res.Duration)
		return res, timeoutAdvisory(snap.runner, root, res.Duration)
	}
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	return res, ""
}

// withNote appends an advisory's trailing note — today only the widening one
// — leaving the line untouched when there is nothing to add, so an ordinary
// run still renders exactly one line.
func withNote(line, note string) string {
	if note == "" {
		return line
	}
	return line + "\n" + note
}
