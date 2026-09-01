package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// The edit hook's cargo path, split into a BUILD phase (`--no-run`) and a
// RUN phase under ONE foreground budget. Whichever phase is still going when
// the budget expires keeps running, detached, and is reported by the next
// hook — the alternative (killing it) is what produced 268 timed-out edit
// runs that established nothing.

// deferPhases is the wiring switch: the CLI turns it on for the real hook.
// Off by default so a unit test's injected SuiteRunner is never silently
// replaced by a real process spawn.
var deferPhases atomic.Bool

// EnableDeferredPhases turns detached build/run phases on. Exported for
// internal/cli, which owns the hook wiring.
func EnableDeferredPhases(on bool) { deferPhases.Store(on) }

// DeferredPhasesEnabled reports the current setting, so the CLI's own test
// can prove the hook wires it.
func DeferredPhasesEnabled() bool { return deferPhases.Load() }

// spawnPhaseFn / killDeferredFn are the process seams: production starts a
// detached `aphrollo tdd runphase` and kills by pid; a test substitutes both.
var (
	spawnPhaseFn   = spawnPhase
	killDeferredFn = killDeferred
)

// deferredEditOutcome is what the deferral path reports back to PostEdit:
// either a finished SuiteResult, or a notice that a phase is still running.
type deferredEditOutcome struct {
	res      SuiteResult
	deferred bool
	notice   string
	prefix   string // "deferred: " once a result came from a detached phase
}

// runEditPhases executes the edit's tests as build-then-run inside budget,
// deferring whatever does not finish. It reports deferred=true when a phase
// was left running, in which case res is meaningless.
func runEditPhases(runner Runner, root, headSHA, fileHash, session string, budget time.Duration) deferredEditOutcome {
	deadline := time.Now().Add(budget)
	build := DeferredJob{
		Project: root, Phase: "build", Dir: runnerDir(runner, root),
		Runner: phaseArgv(runner, "build"), HeadSHA: headSHA, FileHash: fileHash, Session: session,
	}
	if !splittable(runner) {
		// Only cargo can build tests without running them; `go test --no-run`
		// is not a flag. One phase, still deferrable.
		single := build
		single.Phase = "run"
		single.Runner = phaseArgv(runner, "run")
		out, done := startAndWait(single, time.Until(deadline))
		if !done {
			return deferredEditOutcome{deferred: true, notice: buildingLine(root, "run", 0)}
		}
		return deferredEditOutcome{res: phaseSuiteResult(single, out)}
	}
	out, done := startAndWait(build, time.Until(deadline))
	if !done {
		return deferredEditOutcome{deferred: true, notice: buildingLine(root, "build", 0)}
	}
	if out.ExitCode != 0 {
		// A failed build IS the answer — the same red the foreground run
		// would have produced, classified from the same output.
		return deferredEditOutcome{res: phaseSuiteResult(build, out)}
	}
	runPhase := build
	runPhase.Phase = "run"
	runPhase.Runner = phaseArgv(runner, "run")
	out, done = startAndWait(runPhase, time.Until(deadline))
	if !done {
		return deferredEditOutcome{deferred: true, notice: buildingLine(root, "run", 0)}
	}
	return deferredEditOutcome{res: phaseSuiteResult(runPhase, out)}
}

// startAndWait spawns a phase and waits up to budget for it to finish. A
// budget that has already run out still SPAWNS: the point is to keep the
// work going, not to skip it.
func startAndWait(j DeferredJob, budget time.Duration) (PhaseOutcome, bool) {
	started, ok := spawnPhaseFn(j)
	if !ok {
		return PhaseOutcome{}, false
	}
	return waitPhase(started, budget)
}

// harvestDeferred deals with a job left over from an earlier hook. It
// returns an advisory to print (when there is something to say), and
// startFresh=true when the caller should go on to run this edit's own
// phases (nothing was pending, or what was pending is stale/abandoned).
func harvestDeferred(root, headSHA, fileHash, session string, budget time.Duration, state *sessionState, statePath string) (advisory string, startFresh bool) {
	j, ok := loadDeferredJob(root)
	if !ok {
		return "", true
	}
	out, done := deferredResult(j)
	if !done {
		if deferredExpired(j, time.Now()) {
			// The one kill: a phase that outlived any plausible build. A run
			// phase that got this far is the ONLY thing that counts as a
			// timeout — the suite really did fail to finish.
			killDeferredFn(j)
			clearDeferredJob(root)
			if j.Phase == "run" && state != nil {
				state.stampTimeout(root, headSHA)
				_ = state.save(statePath)
			}
			appendGateLog("postedit", root, strings.Join(j.Runner, " "), "deferred-abandoned", time.Since(j.Started))
			return "", true
		}
		// Still working: never kill it, just record that the source moved on.
		if j.FileHash != fileHash {
			markDeferredDirty(root, fileHash)
		}
		return buildingLine(root, j.Phase, time.Since(j.Started)), false
	}

	clearDeferredJob(root)
	if !deferredMatchesSource(j, headSHA, fileHash) {
		// The answer is about code that is no longer on disk (the edit moved
		// on while it ran). Say nothing about it and rebuild.
		return "", true
	}
	if j.Phase == "build" && out.ExitCode == 0 {
		// The expensive half is done and warm — run the tests now.
		runPhase := j
		runPhase.Phase = "run"
		runPhase.Runner = phaseArgvFromBuild(j.Runner)
		runPhase.Log, runPhase.Result = "", ""
		runOut, ranDone := startAndWait(runPhase, budget)
		if !ranDone {
			return buildingLine(root, "run", 0), false
		}
		return markDeferred(editResultAdvisory(runPhase, runOut, root, state, statePath, headSHA)), false
	}
	return markDeferred(editResultAdvisory(j, out, root, state, statePath, headSHA)), false
}

// editResultAdvisory turns a finished phase into the same advisory a
// foreground run would have produced, and stamps the same session state, so
// a deferred answer reads exactly like a prompt one.
func editResultAdvisory(j DeferredJob, out PhaseOutcome, root string, state *sessionState, statePath, headSHA string) string {
	res := phaseSuiteResult(j, out)
	runner := runnerFromArgv(j.Runner, j.Dir)
	prev := []string(nil)
	if state != nil {
		if ps, ok := state.ByProject[root]; ok {
			prev = ps.FailingTests
		}
	}
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	outcome := ClassifyOutcome(res.Passed, res.Output, prev)
	if state != nil {
		state.stamp(root, projectState{
			Outcome:      string(outcome),
			FailingTests: ExtractFailingTests(res.Output),
			Runner:       j.Runner,
		})
		_ = state.save(statePath)
	}
	appendGateLog("postedit", root, strings.Join(j.Runner, " "), string(outcome), res.Duration)
	if outcome.IsRed() {
		return redSummary(runner, root, outcome, res.Output)
	}
	return passAdvisory(runner, root, outcome, res.Output, res.Duration, prev)
}

// markDeferred labels an advisory as coming from work that finished after an
// earlier hook returned, without stacking a second "tdd:" prefix on the line.
func markDeferred(advisory string) string {
	if rest, ok := strings.CutPrefix(advisory, "tdd: "); ok {
		return "tdd: deferred " + rest
	}
	return "tdd: deferred " + advisory
}

// buildingLine is the ONE line an edit gets when its work is still running.
// It names the crate and how long it has been going, so a session can tell
// "started just now" from "this is the same build as five edits ago".
func buildingLine(root, phase string, elapsed time.Duration) string {
	if elapsed <= 0 {
		return fmt.Sprintf("tdd: → BUILDING (deferred; %s %s phase — result at the next hook)", root, phase)
	}
	return fmt.Sprintf("tdd: → BUILDING (deferred; %s %s phase, %.0fs so far — result at the next hook)", root, phase, elapsed.Seconds())
}

// phaseSuiteResult maps a wrapper's outcome plus its log onto the
// SuiteResult the rest of the gate speaks.
func phaseSuiteResult(j DeferredJob, out PhaseOutcome) SuiteResult {
	res := SuiteResult{
		Passed:   out.ExitCode == 0,
		Output:   deferredLog(j),
		Duration: time.Duration(out.Seconds * float64(time.Second)),
	}
	if !res.Passed {
		res.Err = fmt.Sprintf("exit status %d", out.ExitCode)
	}
	return res
}

// splittable reports whether a runner can build its tests without running
// them, which is what makes a separate build phase possible at all.
func splittable(r Runner) bool { return r.Cmd == "cargo" }

// phaseArgv builds one phase's argv from the edit's runner: the build phase
// is the same command with --no-run, the run phase is the command itself.
func phaseArgv(r Runner, phase string) []string {
	argv := append([]string{r.Cmd}, r.Args...)
	if phase == "build" {
		return append(argv, "--no-run")
	}
	return argv
}

// phaseArgvFromBuild recovers the run-phase argv from a recorded build one.
func phaseArgvFromBuild(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == "--no-run" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func runnerFromArgv(argv []string, dir string) Runner {
	if len(argv) == 0 {
		return Runner{}
	}
	return Runner{Cmd: argv[0], Args: argv[1:], Dir: dir}
}

func runnerDir(r Runner, root string) string {
	if r.Dir != "" {
		return r.Dir
	}
	return root
}

// killDeferred ends an abandoned phase. Best-effort: the process may already
// be gone, and a failure here only leaves a process the OS will reap.
func killDeferred(j DeferredJob) {
	if j.PID <= 0 {
		return
	}
	if p, err := os.FindProcess(j.PID); err == nil {
		_ = p.Kill()
	}
}

// headSHAFor is the current commit of root's repo, "" outside a repo — the
// first half of "does this result describe the code on disk now".
func headSHAFor(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// postEditDeferred is the edit hook's deferred path: harvest whatever the
// previous hook left running, then run this edit's own build and run phases
// inside the one foreground budget. It reports exactly one line, like every
// other PostEdit path.
func postEditDeferred(snap stateSnapshot, root, target, headSHA, session string) string {
	budget := PostEditBudget()
	fileHash := fileContentHash(target)
	advisory, fresh := harvestDeferred(root, headSHA, fileHash, session, budget, snap.state, snap.statePath)
	if !fresh {
		return advisory
	}
	out := runEditPhases(snap.runner, root, headSHA, fileHash, session, budget)
	if out.deferred {
		appendGateLog("postedit", root, cmdString(snap.runner), "deferred", 0)
		return out.notice
	}
	res := out.res
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	outcome := ClassifyOutcome(res.Passed, res.Output, snap.prevFailing)
	if snap.state != nil {
		snap.state.stamp(root, projectState{
			Outcome:      string(outcome),
			FailingTests: ExtractFailingTests(res.Output),
			Runner:       append([]string{snap.runner.Cmd}, snap.runner.Args...),
			Fingerprint:  snap.fingerprint,
		})
		_ = snap.state.save(snap.statePath)
	}
	if res.Passed {
		if h := worktreeStateHash(root); h != "" {
			mechCacheAdd(mechKey(root, h, snap.runner))
		}
	}
	appendGateLog("postedit", root, cmdString(snap.runner), string(outcome), res.Duration)
	if outcome.IsRed() {
		return redSummary(snap.runner, root, outcome, res.Output)
	}
	return passAdvisory(snap.runner, root, outcome, res.Output, res.Duration, snap.prevFailing)
}

// promptHarvest reports a deferred job that finished since the last hook, for
// a session that stopped editing and just talks. It answers about the CURRENT
// commit only — a result from another HEAD describes code that is not there.
func promptHarvest(session, cwd string) string {
	if cwd == "" {
		return ""
	}
	root := findRootFrom(cwd)
	if root == "" {
		return ""
	}
	j, ok := loadDeferredJob(root)
	if !ok {
		return ""
	}
	out, done := deferredResult(j)
	if !done {
		return ""
	}
	clearDeferredJob(root)
	if j.Dirty || j.HeadSHA != headSHAFor(root) {
		return ""
	}
	state, statePath := loadSession(session)
	return markDeferred(editResultAdvisory(j, out, root, state, statePath, j.HeadSHA))
}
