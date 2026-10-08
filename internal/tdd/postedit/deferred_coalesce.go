package postedit

import (
	"fmt"
	"time"
)

// Coalescing: a run that is already going for the same tree state answers
// every edit that asks for it again. Edits that land together (a batch of
// tool calls, two sessions in one lane) each used to start their own copy of
// the same package's tests, minutes of duplicate load for one verdict.
//
// Only an IDENTICAL request coalesces: the same project, phase, command,
// working directory, HEAD and tree-state hash. A run started before the tree
// moved describes older code (deferredMatchesSource), so it never answers
// for the newer state, and a different package is a different run.

// liveTwin finds a run another hook started that j would only repeat: no
// result yet, not marked stale, and its process confirmed alive
// (deferredJobLive; a record whose process is gone answers nothing).
func liveTwin(j DeferredJob) (DeferredJob, bool) {
	now := time.Now()
	project := normalizeProjectPath(j.Project)
	for _, o := range allDeferredJobRecords() {
		if o.Dirty || o.Phase != j.Phase || o.Dir != j.Dir ||
			o.HeadSHA != j.HeadSHA || o.FileHash != j.FileHash ||
			!sameRunArgv(o.Runner, j.Runner) || normalizeProjectPath(o.Project) != project {
			continue
		}
		if _, done := deferredResult(o); done || !deferredJobMaybeLive(o, now) {
			continue
		}
		return o, true
	}
	return DeferredJob{}, false
}

// coalescedLine is the line an edit gets when its run was not started because
// twin is already running the same one. Like BUILDING it is no verdict: the
// code was not tested by this hook, and the escape names where the answer
// lands.
func coalescedLine(root string, twin DeferredJob) string {
	return fmt.Sprintf("gate: → BUILDING (deferred; %s %s phase — the identical run for this tree state is already running, not started again; %s)",
		root, twin.Phase, buildingEscapeFor(root))
}

// heldOutcome is the outcome of a phase that was not started or did not
// finish inside the budget: coalesced into twin, or still running.
func heldOutcome(root, phase string, status phaseStatus, twin DeferredJob) deferredEditOutcome {
	if status == phaseCoalesced {
		return deferredEditOutcome{deferred: true, notice: coalescedLine(root, twin)}
	}
	return deferredEditOutcome{deferred: true, notice: buildingLine(root, phase, 0)}
}
