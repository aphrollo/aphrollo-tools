package tdd

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
	saved, ok := loadDeferredJob(j.Project)
	if !ok {
		return j, false
	}
	_ = os.Remove(saved.Result)

	cmd := exec.Command(self, "tdd", "runphase", "--job", deferredJobPath(saved.Project))
	cmd.Dir = saved.Dir
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = detachedAttrs()
	if err := cmd.Start(); err != nil {
		return saved, false
	}
	saved.PID = cmd.Process.Pid
	saved.Started = time.Now()
	saveDeferredJob(saved)
	_ = cmd.Process.Release()
	return saved, true
}

// waitPhase waits up to budget for a spawned phase to write its result. A
// false return means "still running" — never "failed": the job stays on disk
// for the next hook to harvest.
func waitPhase(j DeferredJob, budget time.Duration) (PhaseOutcome, bool) {
	deadline := time.Now().Add(budget)
	for {
		if out, done := deferredResult(j); done {
			clearDeferredJob(j.Project)
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
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure})
		return 0
	}
	log, err := os.Create(j.Log)
	if err != nil {
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure})
		return 0
	}
	defer log.Close()

	r := runnerFromArgv(j.Runner, j.Dir)
	slot, release, held := acquireBuildSlot(runnerTargetDir(r, j.Project), deferredSlotWait())
	if !held {
		// Building without a slot would compile into a target dir another
		// build owns, and the shimmed cargo inside would queue on the very
		// slot this phase could not get.
		fmt.Fprintln(log, "aphrollo: no build slot came free for this phase")
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: phaseSetupFailure, Seconds: time.Since(start).Seconds()})
		return 0
	}
	defer release()
	WriteBuildSlotOwner(slot, cmdString(r), j.Dir)
	defer RemoveBuildSlotOwner(slot)
	defer setBuildJobs(slot.Jobs)()

	// The abandon clock starts HERE, not when the hook spawned this: time
	// spent queuing is not time spent building, and charging it made a phase
	// killable the moment it finally started.
	start = time.Now()
	stampDeferredStart(j.Project, start)

	// The child must know this process already holds the slot: with the
	// cargo-queue shim on PATH, "cargo" resolves to the shim, which would
	// otherwise queue behind THIS phase's own slot record and never run.
	prevHeld, hadHeld := os.LookupEnv(BuildLockHeldEnv)
	os.Setenv(BuildLockHeldEnv, "1")
	cmd := exec.Command(j.Runner[0], j.Runner[1:]...)
	cmd.Dir = j.Dir
	cmd.Env = suiteEnv()
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

// phaseSetupFailure is the exit code the wrapper reports when the phase
// never ran at all (no command, no log, no slot). It is distinct from a
// test failure only in the log line beside it; both are "not green".
const phaseSetupFailure = 125

// stampDeferredStart moves the job's clock to when the build actually began.
func stampDeferredStart(root string, at time.Time) {
	j, ok := loadDeferredJob(root)
	if !ok {
		return
	}
	j.Started = at
	saveDeferredJob(j)
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
