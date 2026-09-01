package tdd

import (
	"errors"
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
	if !ok || len(j.Runner) == 0 {
		return 0
	}
	start := time.Now()
	log, err := os.Create(j.Log)
	if err != nil {
		return 0
	}
	defer log.Close()

	r := runnerFromArgv(j.Runner, j.Dir)
	slot, release, held := acquireBuildSlot(runnerTargetDir(r, j.Project), deferredSlotWait())
	if held {
		defer release()
		WriteBuildSlotOwner(slot, cmdString(r), j.Dir)
		defer RemoveBuildSlotOwner(slot)
		defer setBuildJobs(slot.Jobs)()
	}

	cmd := exec.Command(j.Runner[0], j.Runner[1:]...)
	cmd.Dir = j.Dir
	cmd.Env = suiteEnv()
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

// deferredSlotWait bounds how long a detached phase queues for a build slot:
// the same maximum that bounds the phase itself, so a job cannot sit in the
// queue past the point where the next hook gives up on it.
func deferredSlotWait() time.Duration { return deferredMax() }

func readJobFile(path string) (DeferredJob, bool) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return DeferredJob{}, false
	}
	return decodeJob(data)
}
