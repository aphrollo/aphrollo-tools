package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
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

// spawnPhaseFn / killDeferredFn / processStartTimeFn are the process seams:
// production starts a detached `aphrollo tdd runphase`, kills by pid, and
// queries the OS for a live pid's creation time; a test substitutes all three.
var (
	spawnPhaseFn       = spawnPhase
	killDeferredFn     = killDeferred
	processStartTimeFn = processStartTime
)

// deferredEditOutcome is what the deferral path reports back to PostEdit:
// either a finished SuiteResult, or a notice that a phase is still running.
type deferredEditOutcome struct {
	res      SuiteResult
	deferred bool
	notice   string
	// spawnFailed says nothing is running: reporting BUILDING there is a
	// lie, and the job record it would leave expires into a bogus timeout
	// streak for a run that never started.
	spawnFailed bool
	// infra says the phase DID spawn and finish, but RunPhase's own setup
	// failed before the phase's command ever ran (no build slot came free,
	// or it could not even open its log file) — out.SetupFailed was true.
	// Like spawnFailed, this is never a real test result: ClassifyOutcome
	// must not see it, or a capacity refusal reads as a genuine assertion
	// failure (issues #350, #354).
	infra bool
	// job is the record the phase ran under, carried back so the verdict
	// line can name the run it judges rather than only the build that
	// blocked it (issue #583): the caller has the edit's Runner, but not
	// which PHASE of it the failure belongs to.
	job DeferredJob
}

// finishedEditOutcome turns a completed phase into the outcome runEditPhases
// reports, routing a RunPhase setup failure (out.SetupFailed) to infra
// instead of letting it masquerade as a real (if ugly) test result.
func finishedEditOutcome(j DeferredJob, out PhaseOutcome) deferredEditOutcome {
	if out.SetupFailed {
		return deferredEditOutcome{res: phaseSuiteResult(j, out), infra: true, job: j}
	}
	return deferredEditOutcome{res: phaseSuiteResult(j, out)}
}

// phaseStatus is what became of a spawn: finished inside the budget, still
// running (deferred), or never started.
type phaseStatus int

const (
	phaseFinished phaseStatus = iota
	phaseRunning
	phaseFailedToStart
)

// runEditPhases executes the edit's tests as build-then-run inside budget,
// deferring whatever does not finish. It reports deferred=true when a phase
// was left running, in which case res is meaningless.
func runEditPhases(runner Runner, root, target, headSHA, fileHash, session, editID string, budget time.Duration) deferredEditOutcome {
	deadline := time.Now().Add(budget)
	build := DeferredJob{
		Project: root, Phase: "build", Dir: runnerDir(runner, root),
		Runner: phaseArgv(runner, "build"), HeadSHA: headSHA, FileHash: fileHash,
		File: target, Session: session, EditID: editID,
	}
	if !splittable(runner) {
		// Only cargo can build tests without running them; `go test --no-run`
		// is not a flag. One phase, still deferrable.
		single := build
		single.Phase = "run"
		single.Runner = phaseArgv(runner, "run")
		started, out, status := startAndWait(single, time.Until(deadline))
		if status == phaseFailedToStart {
			return deferredEditOutcome{spawnFailed: true}
		}
		if status == phaseRunning {
			return deferredEditOutcome{deferred: true, notice: buildingLine(root, "run", 0)}
		}
		return finishedEditOutcome(started, out)
	}
	startedBuild, out, status := startAndWait(build, time.Until(deadline))
	if status == phaseFailedToStart {
		return deferredEditOutcome{spawnFailed: true}
	}
	if status == phaseRunning {
		return deferredEditOutcome{deferred: true, notice: buildingLine(root, "build", 0)}
	}
	if out.ExitCode != 0 {
		// A failed build IS the answer — the same red the foreground run
		// would have produced, classified from the same output. Unless
		// RunPhase itself never got to invoke the build at all (no slot),
		// which finishedEditOutcome routes to infra instead.
		return finishedEditOutcome(startedBuild, out)
	}
	runPhase := build
	runPhase.Phase = "run"
	runPhase.Runner = phaseArgv(runner, "run")
	startedRun, out, status := startAndWait(runPhase, time.Until(deadline))
	if status == phaseFailedToStart {
		return deferredEditOutcome{spawnFailed: true}
	}
	if status == phaseRunning {
		return deferredEditOutcome{deferred: true, notice: buildingLine(root, "run", 0)}
	}
	return finishedEditOutcome(startedRun, out)
}

// startAndWait spawns a phase and waits up to budget for it to finish. A
// budget that has already run out still SPAWNS: the point is to keep the
// work going, not to skip it.
func startAndWait(j DeferredJob, budget time.Duration) (DeferredJob, PhaseOutcome, phaseStatus) {
	started, ok := spawnPhaseFn(j)
	if !ok {
		clearDeferredJob(j.Session, j.Project)
		return j, PhaseOutcome{}, phaseFailedToStart
	}
	out, done := waitPhase(started, budget)
	if !done {
		return started, out, phaseRunning
	}
	return started, out, phaseFinished
}

// harvestDeferred deals with a job left over from an earlier hook. It
// returns an advisory to print (when there is something to say), and
// startFresh=true when the caller should go on to run this edit's own
// phases (nothing was pending, or what was pending is stale/abandoned).
func harvestDeferred(root, headSHA, fileHash, session string, budget time.Duration, state *sessionState, statePath string) (advisory string, startFresh bool) {
	deadline := time.Now().Add(budget)
	j, ok := loadDeferredJob(session, root)
	if !ok {
		return "", true
	}
	out, done := deferredResult(j)
	if !done {
		if deferredExpired(j, time.Now()) {
			// The one kill: a phase that outlived any plausible build. A run
			// phase that got this far is the ONLY thing that counts as a
			// timeout — the suite really did fail to finish. Still gated on
			// pidStillOurs: the ceiling here is minutes, not 24h, but the same
			// recycle is possible in miniature.
			if pidStillOurs(j) {
				killDeferredFn(j)
			}
			clearDeferredJob(session, root)
			if j.Phase == "run" && state != nil {
				state.stampTimeout(root, headSHA)
				_ = state.save(statePath)
			}
			elapsed := time.Since(j.Started)
			appendGateLog("postedit", root, strings.Join(j.Runner, " "), DeferredAbandoned, elapsed)
			// The abandonment is the VERDICT for that work, and the only one
			// it will ever get: an inconclusive one. It used to be written to
			// the gate log and nowhere else, so the session saw only the
			// fresh run's BUILDING line and read "in progress" where the
			// truth was "the previous run died without testing your code"
			// (issue #571).
			return deferredAbandonedLine(root, j.Phase, elapsed), true
		}
		// Still working: never kill it, just record that the source moved on.
		if j.FileHash != fileHash {
			markDeferredDirty(session, root, fileHash)
		}
		return buildingLine(root, j.Phase, time.Since(j.Started)), false
	}

	clearDeferredJob(session, root)
	if !deferredMatchesSource(j, headSHA, fileHash) {
		// The answer is about code that is no longer on disk (the edit moved
		// on while it ran): rebuild, and report it labelled as measured on
		// an earlier tree state — never dropped, never passed off as current.
		return staleVerdictLine(j, out), true
	}
	if j.Phase == "build" && out.ExitCode == 0 {
		// The expensive half is done and warm — run the tests now.
		runPhase := j
		runPhase.Phase = "run"
		runPhase.Runner = phaseArgvFromBuild(j.Runner)
		runPhase.Log, runPhase.Result = "", ""
		startedRun, runOut, status := startAndWait(runPhase, time.Until(deadline))
		if status == phaseFailedToStart {
			appendGateLog("postedit", root, strings.Join(runPhase.Runner, " "), InfraFailed, 0)
			return spawnFailedLine(root, "run"), false
		}
		if status == phaseRunning {
			return buildingLine(root, "run", 0), false
		}
		return harvestAdvisory(startedRun, runOut, root, state, statePath, time.Until(deadline)), false
	}
	return harvestAdvisory(j, out, root, state, statePath, time.Until(deadline)), false
}

// editResultAdvisory turns a finished phase into the same advisory a
// foreground run would have produced, and stamps the same session state, so
// a deferred answer reads exactly like a prompt one.
//
// prev is read through state.prevFailing against the fingerprint computed
// HERE, at harvest — not the bare FailingTests field. A foreground run gates
// its own prevFailing on fingerprintsMatch (branch, HEAD, index mtime); this
// path used to skip that gate entirely, so a `git add`, stash or partial
// staging between the job's spawn and its harvest left a stale failing set
// in place to mask a genuinely new failure as NoDelta (issue #295). Every
// foreground path stamps the fingerprint it read alongside the outcome; this
// one now does too, so the NEXT harvest has something real to compare against
// rather than always missing on a nil fingerprint.
func editResultAdvisory(j DeferredJob, out PhaseOutcome, root string, state *sessionState, statePath, headSHA string) string {
	res := phaseSuiteResult(j, out)
	if out.SetupFailed {
		// RunPhase's own setup failed (no build slot, no log file) before the
		// phase's command ever ran: nothing about the TEST is under
		// suspicion, so this must never reach ClassifyOutcome, and must never
		// overwrite the last REAL outcome in state — same posture as a
		// timeout (issues #350, #354).
		appendGateLog("postedit", root, strings.Join(j.Runner, " "), InfraFailed, res.Duration)
		return infraFailureLine(root, j, res)
	}
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	return judgeEditResult(runnerFromArgv(j.Runner, j.Dir), j.File, j.EditID, res, root, state, statePath)
}

// judgeEditResult is editResultAdvisory's verdict half, for a finished run
// that is not an infra failure: classify, stamp, log and render, exactly as
// a foreground run would. Split out so a widened rung a harvest ran itself
// (harvestAdvisory) is judged by the same code as a harvested job.
func judgeEditResult(runner Runner, file, editID string, res SuiteResult, root string, state *sessionState, statePath string) string {
	argv := append([]string{runner.Cmd}, runner.Args...)
	fp := computeFingerprint(root)
	prev := []string(nil)
	if state != nil {
		prev = state.prevFailing(root, fp)
	}
	if line := foreignBuildAdvisory(root, file, cmdString(runner), res); line != "" {
		return line
	}
	outcome := classifyRunOutcome(runner, root, res, prev)
	if state != nil {
		state.stamp(root, projectState{
			Outcome:      string(outcome),
			FailingTests: ExtractFailingTests(res.Output),
			Runner:       argv,
			Fingerprint:  fp,
		})
		_ = state.save(statePath)
	}
	logSuiteVerdict("postedit", root, cmdString(runner), string(outcome), res)
	recordEditVerdict(root, editID, cmdString(runner), outcome, res.Output)
	if outcome.IsRed() {
		return redSummary(runner, root, outcome, res.Output)
	}
	return passAdvisory(runner, root, outcome, res.Output, res.Duration, prev)
}

// markDeferred labels an advisory as coming from work that finished after an
// earlier hook returned, without stacking a second "gate:" prefix on the line.
func markDeferred(advisory string) string {
	if rest, ok := strings.CutPrefix(advisory, "gate: "); ok {
		return "gate: deferred " + rest
	}
	return "gate: deferred " + advisory
}

// spawnFailedLine reports a phase that never started at all: the hook could
// not even launch the detached runphase wrapper. InfraFailed, not RedBogus —
// see its doc comment for why the two must never be confused.
func spawnFailedLine(root, phase string) string {
	return fmt.Sprintf("gate: → %s (could not start the %s phase in %s — the code was NOT tested)", InfraFailed, phase, root)
}

// sourceIdentity is what a deferred result claims to be about: the whole
// worktree's state, not the one edited file. A single file's hash cannot see
// a change made outside the hook (another session, a script, a rebase), and
// those are exactly the changes that make an answer stale without anyone
// telling the gate. Outside a git repo it falls back to the edited file.
func sourceIdentity(root, target string) string {
	if h := worktreeStateHash(root); h != "" {
		return h
	}
	return fileContentHash(target)
}

// buildingEscape is the way to get a verdict WITHOUT another edit: the trap
// this closes is that "result at the next hook" reads as "in progress",
// which invites a caller to stop and wait, but nothing delivers the verdict
// unless another edit or prompt arrives to harvest it — the very thing
// waiting removes. Committing is one answer that needs nothing new from the
// caller: the precommit gate runs the suite itself and judges the work.
// `gate status --wait` is the other, and the one actually meant for a
// caller who wants the verdict NOW: it blocks on this same job rather than
// spawning a second run that queues behind the first (issue #430) — a
// rerun is deliberately NOT offered here any more, since it is exactly the
// double run the deferred phase exists to avoid.
const buildingEscape = "no verdict until then — commit and precommit will judge it, or run: aphrollo gate status --wait"

// buildingEscapeFor is buildingEscape naming the tree the job was recorded
// under. A bare `gate status --wait` resolves the checkout from the shell
// cwd, which the harness resets between calls — to the primary checkout,
// whose tree holds none of a lane's jobs (issue #732) — so the command the
// line offers carries its own path.
func buildingEscapeFor(root string) string {
	return buildingEscape + " " + shellPath(root)
}

// buildingLine is the ONE line an edit gets when its work is still running.
// It names the crate and how long it has been going, so a session can tell
// "started just now" from "this is the same build as five edits ago", and it
// carries buildingEscape so a caller who stops editing here has a documented
// move rather than a wait with no way out.
func buildingLine(root, phase string, elapsed time.Duration) string {
	if elapsed <= 0 {
		return fmt.Sprintf("gate: → BUILDING (deferred; %s %s phase — result at the next hook; %s)", root, phase, buildingEscapeFor(root))
	}
	return fmt.Sprintf("gate: → BUILDING (deferred; %s %s phase, %.0fs so far — result at the next hook; %s)", root, phase, elapsed.Seconds(), buildingEscapeFor(root))
}

// phaseSuiteResult maps a wrapper's outcome plus its log onto the
// SuiteResult the rest of the gate speaks.
func phaseSuiteResult(j DeferredJob, out PhaseOutcome) SuiteResult {
	res := SuiteResult{
		Passed:   out.ExitCode == 0,
		Output:   deferredLog(j),
		Duration: time.Duration(out.Seconds * float64(time.Second)),
		Dir:      runnerDir(Runner{Dir: j.Dir}, j.Project),
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
		if hasNoRunFlag(argv) {
			return argv
		}
		return append(argv, "--no-run")
	}
	// A `go test` phase names its own -timeout, above the deferral ceiling:
	// go's default 10m otherwise collides with that ceiling and both answers
	// are lost (deferred_verdict.go, issue #571). Only cargo is splittable,
	// so a go run never reaches the build branch above.
	return withDeferredGoTimeout(argv)
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
	if err := killTreeFn(j.PID); err != nil {
		// The tree killer is the one that reaches cargo/rustc; falling back
		// to the bare pid at least stops the wrapper from holding its slot.
		if p, ferr := os.FindProcess(j.PID); ferr == nil {
			_ = p.Kill()
		}
	}
}

// killTreeFn is the process-tree kill seam, so a test can prove the whole
// tree is targeted without spawning one.
var killTreeFn = proc.KillTree

// pidIdentityTolerance is how far a live pid's OS-reported creation time may
// drift from the one this job recorded and still count as the same process.
// It exists only to absorb measurement noise (ps's lstart is second-
// resolution, and the spawn-time sample is taken a few instructions after
// the OS actually created the process) — nowhere near enough to paper over
// an actual recycle, which sits hours or days away.
const pidIdentityTolerance = 5 * time.Second

// pidStillOurs reports whether the live process at j.PID is still the one
// this job recorded, not whatever the OS handed the same integer to after
// ours exited. A kill site with no evidence either way (PIDCreatedAt never
// recorded, or the live query fails) still gets to kill: that keeps the
// crash backstop working on an old-format record or a platform where the
// query is unavailable, matching the rest of this file's best-effort
// posture. Only a POSITIVE mismatch — both times known, and they disagree —
// withholds the kill.
func pidStillOurs(j DeferredJob) bool {
	if j.PIDCreatedAt.IsZero() {
		return true
	}
	live, ok := processStartTimeFn(j.PID)
	if !ok {
		return true
	}
	drift := live.Sub(j.PIDCreatedAt)
	return drift.Abs() <= pidIdentityTolerance
}

// reapSessionDeferredJobs ends every deferred phase the given session
// started, across every project it touched, and drops their records.
// EndSession calls this before it drops the session's own state file: a job
// is keyed session+project, so loadDeferredJob is only ever looked up again
// by the SAME session's own next hook (harvestDeferred, promptHarvest) — and
// a session that just ended will not fire another hook. Left alone, a job
// still running at that point would run forever, holding its target lock
// and a build slot with nothing left to ever harvest or kill it (the same
// gap the 24h file-age sweep does not close: it deletes the evidence, never
// the process). Best-effort like killDeferred itself: a job whose wrapper
// already finished is killed too rather than paying to parse its result
// first — the process is already gone, so the call is a harmless no-op.
func reapSessionDeferredJobs(session string) int {
	reaped := 0
	for _, j := range sessionDeferredJobs(session) {
		if j.PID > 0 {
			killDeferredFn(j)
		}
		clearDeferredJob(j.Session, j.Project)
		reaped++
	}
	return reaped
}

// headSHAFor is the current commit of root's repo, "" outside a repo — the
// first half of "does this result describe the code on disk now".
func headSHAFor(root string) string {
	out, err := exec.Command(gitBinary(), "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
