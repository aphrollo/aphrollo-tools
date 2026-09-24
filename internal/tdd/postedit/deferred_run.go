package postedit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// phasePollEvery is how often a waiting hook looks for the wrapper's result
// file. Short enough that a fast phase costs no visible latency, long enough
// that a 110s wait is a few hundred stats.
const phasePollEvery = 200 * time.Millisecond

// spawnPhase starts one phase DETACHED, so it outlives the hook process that
// started it: on Windows the hook's exit would otherwise take the whole
// console process group with it. The wrapper (`aphrollo tdd runphase`) owns
// the build slot, the log and the result file.
func spawnPhase(j DeferredJob) (DeferredJob, bool) {
	self, err := os.Executable()
	if err != nil {
		return j, false
	}
	saveDeferredJob(j)
	saved, ok := loadDeferredJob(j.Session, j.Project)
	if !ok {
		return j, false
	}
	_ = os.Remove(saved.Result)

	cmd := exec.Command(self, CmdName, "runphase", "--job", deferredJobPath(saved.Session, saved.Project))
	cmd.Dir = saved.Dir
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1")
	closeStdio := silentStdio(cmd)
	cmd.SysProcAttr = detachedAttrs()
	err = cmd.Start()
	closeStdio()
	if err != nil {
		return saved, false
	}
	saved.PID = cmd.Process.Pid
	spawnTime := time.Now()
	saved.Started = spawnTime
	// Sampled once, right here, and never touched again: it is the identity
	// a later kill site checks the live pid against, not the build's own
	// clock (Started moves to when the build actually began — see
	// stampDeferredStart). Left zero when the OS query fails, which
	// pidStillOurs reads as "unknown" rather than "mismatch".
	if t, ok := processStartTimeFn(saved.PID); ok {
		saved.PIDCreatedAt = t
	}
	recordSpawnedProcess(saved.Session, saved.Project, saved.PID, saved.PIDCreatedAt, spawnTime)
	_ = cmd.Process.Release()
	return saved, true
}

// recordSpawnedProcess persists the pid identity a just-started detached
// phase got, once cmd.Start() has returned. Routed through updateDeferredJob,
// not a bare saveDeferredJob: the detached child reaches stampDeferredStart
// within microseconds (a non-cargo runner skips the build-slot wait entirely
// — see RunPhase), and an unlocked overwrite here from a pre-Start()
// snapshot could land AFTER the child's own locked write and revert Started
// back to spawn time — precisely the "killable the moment it finally
// started" bug stampDeferredStart's own redesign exists to avoid. Started is
// set here only when the child has not already advanced it past zero;
// PID/PIDCreatedAt are always this call's, since nothing else ever sets them.
func recordSpawnedProcess(session, root string, pid int, pidCreatedAt, spawnTime time.Time) {
	updateDeferredJob(session, root, func(cur *DeferredJob) {
		cur.PID = pid
		cur.PIDCreatedAt = pidCreatedAt
		if cur.Started.IsZero() {
			cur.Started = spawnTime
		}
	})
}

// waitPhase waits up to budget for a spawned phase to write its result. A
// false return means "still running" — never "failed": the job stays on disk
// for the next hook to harvest.
func waitPhase(j DeferredJob, budget time.Duration) (PhaseOutcome, bool) {
	deadline := time.Now().Add(budget)
	for {
		if out, done := deferredResult(j); done {
			clearDeferredJob(j.Session, j.Project)
			return out, true
		}
		if !time.Now().Before(deadline) {
			return PhaseOutcome{}, false
		}
		time.Sleep(phasePollEvery)
	}
}

// RunPhase is the detached wrapper's body: it holds the build slot for the
// job's target dir, runs the phase's own command with the slot's job count,
// streams everything into the job's log, and writes the result file that
// tells the next hook the phase is over. It is the ONE writer of that file.
func RunPhase(jobPath string) int {
	j, ok := readJobFile(jobPath)
	if !ok {
		// Nothing readable means no result path either: the hook that spawned
		// this will time the job out rather than wait on it forever.
		return 0
	}
	start := time.Now()
	if len(j.Runner) == 0 {
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure, SetupFailed: true})
		return 0
	}
	log, err := os.Create(j.Log)
	if err != nil {
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure, SetupFailed: true})
		return 0
	}
	defer log.Close()

	r := runnerFromArgv(j.Runner, j.Dir)
	// The TARGET-DIR flock mirrors cargo's own build-directory lock (one
	// build per target dir, ever) and only cargo has such a directory to
	// protect: resolveTargetDir's cargo-shaped fallback would otherwise key
	// a Go run onto <workspace>/target under whatever CARGO_TARGET_DIR the
	// shell happens to export — the SAME target dir every cargo build on
	// the box shares — serialising unrelated Go projects against unrelated
	// cargo builds for no reason (issue #354). But the GLOBAL slots are a
	// box-wide OOM/CPU governor (buildslots.go's header comment), not a
	// cargo-specific one, and a non-cargo runner skipping them entirely —
	// this fix's first version — left them running fully unbounded; a cold
	// review caught it. So: the flock only for cargo, the global cap for
	// everyone.
	if r.Cmd == "cargo" {
		targetDir := runnerTargetDir(r, j.Project)
		// Queued rather than plain: a newer identical request replaces this
		// one while it waits, so repeated edits never stack identical builds
		// behind one target dir (#830).
		slot, release, wait := acquireQueuedBuildSlot(targetDir, deferredSlotWait(), cmdString(r), j.Dir)
		if wait == SlotSuperseded {
			fmt.Fprintf(log, "aphrollo: superseded while queued for %s by a newer identical request (%q in %s), which builds the newer tree state\n", targetDir, cmdString(r), j.Dir)
			writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure, Seconds: time.Since(start).Seconds(), SetupFailed: true})
			return 0
		}
		if wait != SlotHeld {
			// Building without a slot would compile into a target dir another
			// build owns, and the shimmed cargo inside would queue on the very
			// slot this phase could not get. Naming the target dir and its
			// holder is what lets a session read this as "the box was full",
			// not as a red the tests themselves produced.
			fmt.Fprintf(log, "aphrollo: no build slot came free for %s (%s)\n", targetDir, buildSlotHolderDescription(targetDir))
			writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure, Seconds: time.Since(start).Seconds(), SetupFailed: true})
			return 0
		}
		defer release()
		defer setBuildJobs(slot.Jobs)()
	} else {
		// Global slot only: a non-cargo runner has no build directory for
		// the flock to protect, but it still counts against the box's
		// OOM/CPU budget. CARGO_BUILD_JOBS is meaningless to a `go test`/
		// pytest/npm child, so there is no per-slot job count to set here.
		_, release, held := acquireGlobalSlot(deferredSlotWait(), cmdString(r), j.Dir)
		if !held {
			fmt.Fprintf(log, "aphrollo: no build slot came free (%s)\n", globalCapacityHolderDescription())
			writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure, Seconds: time.Since(start).Seconds(), SetupFailed: true})
			return 0
		}
		defer release()
	}

	// The abandon clock starts HERE, not when the hook spawned this: time
	// spent queuing is not time spent building, and charging it made a phase
	// killable the moment it finally started.
	start = time.Now()
	stampDeferredStart(j.Session, j.Project, start)

	// The child must know this process already holds the slot: with the
	// cargo-queue shim on PATH, "cargo" resolves to the shim, which would
	// otherwise queue behind THIS phase's own slot record and never run.
	prevHeld, hadHeld := os.LookupEnv(BuildLockHeldEnv)
	os.Setenv(BuildLockHeldEnv, "1")
	cmd := exec.Command(j.Runner[0], j.Runner[1:]...)
	cmd.Dir = j.Dir
	cmd.Env = suiteEnv(r, j.Project)
	if hadHeld {
		os.Setenv(BuildLockHeldEnv, prevHeld)
	} else {
		os.Unsetenv(BuildLockHeldEnv)
	}
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	writePhaseResult(j.Result, PhaseOutcome{ExitCode: code, Seconds: time.Since(start).Seconds()})
	return 0
}

// phaseSetupFailure is the ExitCode the wrapper reports alongside
// SetupFailed: true when the phase never ran at all (no command, no log, no
// slot) — a human-legible placeholder for a run that produced no real exit
// status, never itself the signal a consumer branches on (that is
// PhaseOutcome.SetupFailed; see its doc comment for why a bare exit-code
// value cannot carry this safely).
const phaseSetupFailure = 125

// stampDeferredStart moves the job's clock to when the build actually began.
// Runs in the detached runphase process, concurrently with a PostToolUse
// hook's markDeferredDirty in the process that spawned it — updateDeferredJob
// (deferred.go) is what keeps the two from clobbering each other.
func stampDeferredStart(session, root string, at time.Time) {
	updateDeferredJob(session, root, func(j *DeferredJob) {
		j.Started = at
	})
}

// deferredSlotWait bounds how long a detached phase queues for a build slot:
// a FRACTION of the phase's own ceiling, so a job that spent its wait in the
// queue still has most of its life left to build in.
func deferredSlotWait() time.Duration { return deferredMax() / 4 }

func readJobFile(path string) (DeferredJob, bool) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return DeferredJob{}, false
	}
	return decodeJob(data)
}
