//go:build windows

package lock

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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
		out.PeakJobMemoryUsed*100 >= limit*jobCapNearPercent
}
