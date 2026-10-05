package postedit

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// postEditDeferred is the edit hook's deferred path: harvest whatever the
// previous hook left running, then run this edit's own build and run phases
// inside the one foreground budget. It reports exactly one line, like every
// other PostEdit path.
func postEditDeferred(snap stateSnapshot, root, target, headSHA, session string) (advisory string, stillRunning bool) {
	budget := PostEditBudget()
	deadline := time.Now().Add(budget)
	fileHash := sourceIdentityInBatch(root, target)
	if line, held := activeRunLine(snap, root, target, session, fileHash); held {
		return line, false
	}
	carried, fresh := harvestDeferred(root, headSHA, fileHash, session, budget, snap.state, snap.statePath)
	if !fresh {
		return carried, false
	}
	// This edit's run starts now and reads the tree as it stands: a request for
	// the same run still waiting has nothing to add. Whatever else waits starts
	// when the slot frees, which the hook's session sweep sees to.
	dropQueuedRun(session, root, runnerArgv(snap.runner), runnerDir(snap.runner, root))
	// Every line below has to carry whatever the harvest already concluded —
	// today only an abandonment, which is a verdict about work this session
	// asked for and has to hear about even though a fresh run is starting
	// (issue #571). One defer beats repeating the fold at eight returns.
	defer func() { advisory = joinDeferredAdvisory(carried, advisory) }()
	// The state the phases are about to compile: their green is recorded
	// under this, and only if the tree is still here when they finish (#813).
	before := worktreeStateHashInBatch(root)
	out := runEditPhases(snap.runner, root, target, headSHA, fileHash, session, snap.editID, budget, snap.touched...)
	if out.spawnFailed {
		AppendGateLog("postedit", root, cmdString(snap.runner), InfraFailed, 0)
		return spawnFailedLine(root, "build"), false
	}
	if out.deferred {
		token := cmp.Or(out.logToken, "deferred")
		AppendGateLog("postedit", root, cmdString(snap.runner), token, 0)
		return out.notice, true
	}
	if out.Infra {
		AppendGateLog("postedit", root, cmdString(snap.runner), InfraFailed, out.res.Duration)
		return infraFailureLine(root, out.job, out.res), false
	}
	markOutcomeSeen(session, out.phase)
	kernelRes := out.res // as read, before the empty-pass adjustment below
	widened := false
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
	// A pytest run whose interpreter lacks a third-party module never reached
	// a test: the environment is what is missing, so it is not a red either.
	if line := missingModuleTerminal(snap.runner, root, pytestRemedyRoot, res); line != "" {
		return line, false
	}
	// A narrowed run that selected nothing climbs the widening ladder in
	// what is left of this same budget; a rung still running when it runs
	// out is left detached and reported by the next hook.
	widenNote := ""
	if postEditSelectedZero(snap.runner, target, res) {
		w := widenDeferredSelection(snap.runner, root, target, headSHA, fileHash, session, snap.editID, deadline, res, snap.touched...)
		if w.terminal != "" {
			return w.terminal, w.running
		}
		snap.runner, res, widenNote = w.runner, w.res, w.note
		kernelRes, widened = w.res, true
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
		mechCacheAddUnmoved(root, before, snap.runner)
	}
	logSuiteVerdict("postedit", root, cmdString(snap.runner), string(outcome), res)
	if widened {
		queueForegroundRun(kernelRes, root, session, snap.editID, out.phase.TreeKey, runnerArgv(snap.runner), string(outcome))
	} else {
		queueShadowRun(out.job.Phase, out.phase, kernelRes, root, session, snap.editID, out.job.Runner, string(outcome))
	}
	recordEditVerdict(root, snap.editID, cmdString(snap.runner), outcome, res.Output)
	if outcome.IsRed() {
		return withLintGuidance(withNote(redSummary(snap.runner, root, outcome, res.Output), widenNote), out.lint), false
	}
	return withLintGuidance(withNote(passAdvisory(snap.runner, root, outcome, res.Output, res.Duration, snap.prevFailing), widenNote), out.lint), false
}

// runnerArgv is a runner's command line.
func runnerArgv(r Runner) []string { return append([]string{r.Cmd}, r.Args...) }

// activeRunLine is the edit's own line when a run of this session is still going
// in the project: the edit is not left with that run's BUILDING line and nothing
// of its own (issue #1188). The run is one of three things. The edit's own run,
// for the tree as it stands: it answers the edit. The edit's own run, started
// for an older tree: it is marked to restart on the newest source, and the line
// says the verdict now coming is about the older one. Or another run: the edit's
// waits in the queue until the slot frees, replacing an older request for the
// same run. A queue at its bound answers QUEUED-SKIPPED. Anything but a run
// still going and within its ceiling is the harvest's, and held is false.
func activeRunLine(snap stateSnapshot, root, target, session, fileHash string) (line string, held bool) {
	j, ok := loadDeferredJob(session, root)
	if !ok {
		return "", false
	}
	if _, done := deferredResult(j); done || !deferredJobMaybeLive(j, time.Now()) {
		return "", false
	}
	argv, dir := runnerArgv(snap.runner), runnerDir(snap.runner, root)
	active := j.Runner
	if j.Phase == "build" {
		active = runArgvAfterBuild(j)
	}
	// The tree moved under a run that is going: its verdict will describe an
	// older state, whichever run the edit is for. It is marked, so the harvest
	// restarts it on the newest source and no coverage it had is lost; the edit
	// is not left to chase it.
	if len(active) == 0 {
		// A record that names no command (one written before the field existed)
		// cannot be told from the edit's own run: the harvest's own path answers,
		// marking it dirty and saying it is building.
		return "", false
	}
	moved := j.FileHash != fileHash
	if moved {
		markDeferredDirty(session, root, fileHash)
		j.Dirty = true
	}
	if slices.Equal(active, phaseArgv(snap.runner, "run")) && j.Dir == dir {
		if moved {
			AppendGateLog("postedit", root, cmdString(snap.runner), "deferred-restart", 0)
			return queuedSameRunLine(snap.runner, root), true
		}
		return runningRunLine(snap.runner, root, j), true
	}
	out := enqueueRun(session, root, queuedRun{Runner: argv, Dir: dir, File: target, EditID: snap.editID, Touched: snap.touched, At: time.Now()})
	if out.full {
		AppendGateLog("postedit", root, cmdString(snap.runner), "queued-skipped", 0)
		return queueFullLine(snap.runner, root), true
	}
	AppendGateLog("postedit", root, cmdString(snap.runner), "queue-waiting", 0)
	return queuedLine(snap.runner, root, active, out), true
}

// runningRunLine is the line of an edit whose own run is already going for the
// tree as it stands: it names the run, so the line is about this edit. A run
// known to be measuring an older state says so and offers no wait (issue #1189).
func runningRunLine(r Runner, root string, j DeferredJob) string {
	if j.Dirty {
		return buildingStaleLine(root, j.Phase, time.Since(j.Started))
	}
	return fmt.Sprintf("gate: %s in %s → BUILDING (deferred; %s phase, %.0fs so far — result at the next hook; %s)",
		cmdString(r), root, j.Phase, time.Since(j.Started).Seconds(), buildingEscapeFor(root))
}
