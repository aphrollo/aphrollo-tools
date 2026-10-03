//go:build windows

package run

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobTree guards a child with a job object that kills on close; a heavy
// child's also carries the memory cap. It has no breakaway flag, so nothing
// the child starts can leave the job, and the child starts suspended and runs
// only once it is inside: a limit applies to allocations made after the
// assignment, and a child let run first could commit its whole working set
// before it was held. taskkill /T misses an MSYS grandchild, and a walk from
// the pid misses a child under load; the job holds every process in it.
//
// A light child is held the same way but only until it is ended: when it
// exits on its own the job lets go of what it left running, as a light child's
// own exit has always done (git leaves its fsmonitor daemon). Only a heavy
// child's exit ends what it left.
type jobTree struct {
	mu    sync.Mutex
	job   windows.Handle
	light bool
	peakB uint64 // the job's peak commit, read just before the job is closed
}

func prepare(cmd *exec.Cmd, heavy bool, memoryMB int64) (tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if heavy && memoryMB > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(memoryMB) << 20
	}
	if err := setJobLimits(job, &info); err != nil {
		_ = windows.CloseHandle(job) // the job was never used
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	return &jobTree{job: job, light: !heavy}, nil
}

func setJobLimits(job windows.Handle, info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)))
	return err
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// holdStart puts CREATE_SUSPENDED back on a command whose Before hook replaced
// its SysProcAttr or its flags. The job join must happen before the child runs:
// a child that ran unsuspended may be gone, or half done, when the join fails,
// and the fallback would start the command a second time. The hook's other
// attributes stay.
func (j *jobTree) holdStart(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

func (j *jobTree) attach(p *os.Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }() // a handle opened here for the assignment
	if err := windows.AssignProcessToJobObject(j.job, h); err != nil {
		return err
	}
	if status, _, _ := ntResumeProcess.Call(uintptr(h)); status != 0 {
		// Whether a failed resume let the child run is not known.
		return mayHaveRun{errors.New("run: could not resume the child after it joined its job")}
	}
	return nil
}

func (j *jobTree) kill() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.job != 0 {
		_ = windows.TerminateJobObject(j.job, 1) // the job is going anyway; a refusal leaves finish to close it
	}
}

func (j *jobTree) finish() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.job == 0 {
		return
	}
	j.peakB = peakJobMemory(j.job)
	if j.light {
		// A light child that has exited leaves what it started alone: with the
		// kill-on-close flag cleared, closing the handle ends nothing. A clear
		// that is refused leaves the flag, and the close then ends the tree.
		_ = setJobLimits(j.job, new(windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION))
	} else {
		_ = windows.TerminateJobObject(j.job, 1) // as in kill
	}
	_ = windows.CloseHandle(j.job) // for a heavy child, closing kills on close even where the terminate was refused
	j.job = 0
}

func (j *jobTree) peak() uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.peakB
}

// peakJobMemory is the most the job's processes committed together; 0 when the
// job cannot be asked.
func peakJobMemory(job windows.Handle) uint64 {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0
	}
	return uint64(info.PeakJobMemoryUsed)
}
