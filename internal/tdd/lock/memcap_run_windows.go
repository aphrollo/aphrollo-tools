//go:build windows

package lock

import (
	"os/exec"
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
	defer windows.CloseHandle(job)
	limit := uintptr(c.MB) << 20
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	info.JobMemoryLimit = limit
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		res.Mode = "none"
		return res, cmd.Run()
	}
	if err := cmd.Start(); err != nil {
		return res, err
	}
	if h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
		if err := windows.AssignProcessToJobObject(job, h); err != nil {
			res.Mode = "none"
		}
		_ = windows.CloseHandle(h)
	} else {
		res.Mode = "none"
	}
	waitErr := cmd.Wait()
	var out windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if waitErr != nil && res.Mode == "job" {
		if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&out)), uint32(unsafe.Sizeof(out)), nil); err == nil &&
			out.PeakJobMemoryUsed*100 >= limit*jobCapNearPercent {
			res.Killed, res.Kills = true, 1
		}
	}
	return res, waitErr
}
