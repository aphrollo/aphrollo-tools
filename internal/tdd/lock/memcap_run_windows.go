//go:build windows

package lock

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// jobCapNearPercent is how close to its limit a run's peak commit has to
// have come for a failing exit to be read as the cap's doing: the OS refuses
// an allocation past the limit rather than killing anything, and the process
// then dies of its own failed allocation.
const jobCapNearPercent = 95

// launchCapped is the Windows enforcer: the child runs in a job object with a
// job memory limit, so an allocation past the cap is refused by the OS.
// The child is assigned right after it starts; anything it spawned in that
// first instant is outside the job, which costs a little coverage and never
// a false kill.
func launchCapped(cmd *exec.Cmd, c MemCap) (CapResult, error) {
	res := CapResult{Cap: c, Mode: "job"}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		res.Mode = "none"
		return res, cmd.Run()
	}
	defer func() { _ = windows.CloseHandle(job) }() // best effort: the run is over, and a leaked handle dies with the process
	limit := uintptr(c.MB) << 20
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	info.JobMemoryLimit = limit
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		res.Mode = "none"
		return res, cmd.Run()
	}
	// The child starts suspended and runs only once it is in the job: a limit
	// applies to allocations made after the assignment, so a child that was
	// let run first could commit its whole working set before it was held.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		return res, err
	}
	if h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid)); err == nil {
		if err := windows.AssignProcessToJobObject(job, h); err != nil {
			res.Mode = "none"
		}
		resumeProcess(h)
		_ = windows.CloseHandle(h)
	} else {
		// A suspended child that cannot be reached is never left suspended.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		res.Mode = "none"
		return res, err
	}
	waitErr := cmd.Wait()
	if waitErr != nil && res.Mode == "job" && jobCapEnded(job, limit) {
		// A mutation tool's runaway worker is refused its allocation and dies
		// alone while the run goes on, as on the unix enforcers; a suite's
		// own death is the whole run's.
		res.Killed, res.Kills = !c.KillLargest, 1
	}
	return res, waitErr
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// resumeProcess lets a process created suspended run.
func resumeProcess(h windows.Handle) {
	_, _, _ = ntResumeProcess.Call(uintptr(h))
}

// jobCapEnded reports whether the job's memory limit is what ended the run: the
// peak commit came within jobCapNearPercent of the limit. A runaway grows in
// steps, so it reaches the limit; one allocation larger than the whole limit is
// refused outright and never raises the peak.
func jobCapEnded(job windows.Handle, limit uintptr) bool {
	var out windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	return windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&out)), uint32(unsafe.Sizeof(out)), nil) == nil &&
		peakNearLimit(uint64(out.PeakJobMemoryUsed), uint64(limit))
}

// peakNearLimit reports whether a peak commit came within jobCapNearPercent of
// the limit, both in bytes.
func peakNearLimit(peak, limit uint64) bool { return peak*100 >= limit*jobCapNearPercent }

// CapRun holds one run to its cap while internal/run starts and ends it. On
// Windows the enforcer is run's own job object (Spec.MemoryMB), which refuses
// an allocation past the cap; what is left to this hook is telling the cap's
// doing from an ordinary failure once the run is over.
type CapRun struct{ cap MemCap }

// NewCapRun is the hook that holds a run to c. The caller also sets the
// child's Spec.MemoryMB to c.MB, which is what enforces it.
func NewCapRun(c MemCap) *CapRun { return &CapRun{cap: c} }

// Before, Started and Ended have nothing to do: the job needs no wrapper and
// no watcher.
func (r *CapRun) Before(*exec.Cmd) {}
func (r *CapRun) Started(int)      {}
func (r *CapRun) Ended()           {}

// Result is what became of the run: ended by the cap when it failed with its
// tree's peak commit within jobCapNearPercent of the limit. A mutation tool's
// runaway worker dies alone while the run goes on, so only a suite's own death
// is the whole run's. A child that never started has no result but the cap.
func (r *CapRun) Result(child *run.Child) CapResult {
	res := CapResult{Cap: r.cap, Mode: "job"}
	if child != nil && child.ExitError() != nil && peakNearLimit(child.PeakMemory(), uint64(r.cap.MB)<<20) {
		res.Killed, res.Kills = !r.cap.KillLargest, 1
	}
	return res
}
