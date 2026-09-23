package tdd

import (
	"fmt"
	"time"
)

// The deferred half of the widening ladder (emptyselection_ladder.go). The
// real edit hook runs its tests as detached build/run phases, so a rung is
// one more pair of phases under the same deadline: it finishes inside the
// budget and is judged here, or it keeps running, detached, and the next
// hook harvests it — climbing on from there if it selected nothing too.

// deferredWidening is what climbing the ladder concluded: the rung whose run
// is the verdict, with its result and the note naming the climb, or a
// terminal line that ends the hook — a rung still running, a phase that
// never started, or a selection still empty at the top of the ladder.
type deferredWidening struct {
	runner   Runner
	res      SuiteResult
	note     string
	terminal string
	running  bool
}

// widenDeferredSelection climbs the ladder above narrow, whose run selected
// nothing, one rung at a time until a rung selects a test or deadline
// passes. A rung spawned at or past the deadline is not skipped: it is left
// running and reported as such, because the work is worth keeping and the
// next hook reports it.
func widenDeferredSelection(narrow Runner, root, target, headSHA, fileHash, session string, deadline time.Time, res SuiteResult) deferredWidening {
	last, lastRes := narrow, res
	steps := postEditWideningSteps(narrow)
	for _, step := range steps {
		out := runEditPhases(step, root, target, headSHA, fileHash, session, time.Until(deadline))
		switch {
		case out.spawnFailed:
			appendGateLog("postedit", root, cmdString(step), InfraFailed, 0)
			return deferredWidening{terminal: spawnFailedLine(root, "build")}
		case out.deferred:
			appendGateLog("postedit", root, cmdString(step), "deferred", 0)
			return deferredWidening{terminal: wideningBuildingLine(last, step, root), running: true}
		case out.infra:
			appendGateLog("postedit", root, cmdString(step), InfraFailed, out.res.Duration)
			return deferredWidening{terminal: infraFailureLine(root, out.job, out.res)}
		}
		wres := out.res
		if treatAsEmptyPass(wres) {
			wres.Passed = true
		}
		last, lastRes = step, wres
		if !selectedZeroTests(step, wres) {
			return deferredWidening{runner: step, res: wres, note: widenedNote(narrow, step)}
		}
	}
	appendGateLog("postedit", root, cmdString(last), NoTestsSelected, lastRes.Duration)
	return deferredWidening{terminal: noTestsSelectedAdvisory(narrow, last, root, len(steps) > 0, lastRes.Duration)}
}

// wideningBuildingLine is the BUILDING line for a rung the budget ran out
// on. Unlike an ordinary BUILDING line it names the command still running
// and the run below it that selected nothing, so the session knows which
// verdict is coming and why the edit's own narrowed run is not it.
func wideningBuildingLine(below, rung Runner, root string) string {
	return fmt.Sprintf("gate: %s in %s → BUILDING (deferred; widened because %s selected 0 tests — result at the next hook; %s)",
		cmdString(rung), root, cmdString(below), buildingEscape)
}

// harvestAdvisory is the advisory for a finished run phase a hook harvested.
// A run that tested something is judged as it always was
// (editResultAdvisory); a run that selected nothing climbs on from its own
// rung within budget, so an empty selection that finished between hooks is
// never reported as the empty-crate green.
func harvestAdvisory(j DeferredJob, out PhaseOutcome, root string, state *sessionState, statePath string, budget time.Duration) string {
	res := phaseSuiteResult(j, out)
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	runner := runnerFromArgv(j.Runner, j.Dir)
	if out.SetupFailed || !selectedZeroTests(runner, res) {
		return markDeferred(editResultAdvisory(j, out, root, state, statePath, j.HeadSHA))
	}
	w := widenDeferredSelection(runner, root, j.File, j.HeadSHA, j.FileHash, j.Session, time.Now().Add(budget), res)
	if w.terminal != "" {
		return w.terminal
	}
	return markDeferred(withNote(judgeEditResult(w.runner, j.File, w.res, root, state, statePath), w.note))
}
