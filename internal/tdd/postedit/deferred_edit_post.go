package postedit

import "time"

// postEditDeferred is the edit hook's deferred path: harvest whatever the
// previous hook left running, then run this edit's own build and run phases
// inside the one foreground budget. It reports exactly one line, like every
// other PostEdit path.
func postEditDeferred(snap stateSnapshot, root, target, headSHA, session string) (advisory string, stillRunning bool) {
	budget := PostEditBudget()
	deadline := time.Now().Add(budget)
	fileHash := sourceIdentity(root, target)
	carried, fresh := harvestDeferred(root, headSHA, fileHash, session, budget, snap.state, snap.statePath)
	if !fresh {
		return carried, false
	}
	// Every line below has to carry whatever the harvest already concluded —
	// today only an abandonment, which is a verdict about work this session
	// asked for and has to hear about even though a fresh run is starting
	// (issue #571). One defer beats repeating the fold at eight returns.
	defer func() { advisory = joinDeferredAdvisory(carried, advisory) }()
	out := runEditPhases(snap.runner, root, target, headSHA, fileHash, session, snap.editID, budget)
	if out.spawnFailed {
		AppendGateLog("postedit", root, cmdString(snap.runner), InfraFailed, 0)
		return spawnFailedLine(root, "build"), false
	}
	if out.deferred {
		AppendGateLog("postedit", root, cmdString(snap.runner), "deferred", 0)
		return out.notice, true
	}
	if out.Infra {
		AppendGateLog("postedit", root, cmdString(snap.runner), InfraFailed, out.res.Duration)
		return infraFailureLine(root, out.job, out.res), false
	}
	res := out.res
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	// A compile check reached no test verdict on the direct path
	// (posttooluse.go) and reaches none here either, so it must not be
	// dressed up as a green.
	if line := buildOnlyTerminal(snap.runner, root, res); line != "" {
		return line, false
	}
	// A narrowed run that selected nothing climbs the widening ladder in
	// what is left of this same budget; a rung still running when it runs
	// out is left detached and reported by the next hook.
	widenNote := ""
	if postEditSelectedZero(snap.runner, res) {
		w := widenDeferredSelection(snap.runner, root, target, headSHA, fileHash, session, snap.editID, deadline, res)
		if w.terminal != "" {
			return w.terminal, w.running
		}
		snap.runner, res, widenNote = w.runner, w.res, w.note
	}
	if line := foreignBuildAdvisory(root, target, cmdString(snap.runner), res); line != "" {
		return line, false
	}
	outcome := classifyRunOutcome(snap.runner, root, res, snap.prevFailing)
	if snap.state != nil {
		snap.state.Stamp(root, projectState{
			Outcome:      string(outcome),
			FailingTests: ExtractFailingTests(res.Output),
			Runner:       append([]string{snap.runner.Cmd}, snap.runner.Args...),
			Fingerprint:  snap.fingerprint,
		})
		_ = snap.state.Save(snap.statePath)
	}
	if res.Passed {
		if h := worktreeStateHash(root); h != "" {
			mechCacheAdd(mechKey(root, h, snap.runner))
		}
	}
	logSuiteVerdict("postedit", root, cmdString(snap.runner), string(outcome), res)
	recordEditVerdict(root, snap.editID, cmdString(snap.runner), outcome, res.Output)
	if outcome.IsRed() {
		return withNote(redSummary(snap.runner, root, outcome, res.Output), widenNote), false
	}
	return withNote(passAdvisory(snap.runner, root, outcome, res.Output, res.Duration, snap.prevFailing), widenNote), false
}
