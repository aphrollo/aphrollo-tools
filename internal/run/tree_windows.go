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

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// jobTree guards a heavy child with a job object that kills on close and
// carries the memory cap. It has no breakaway flag, so nothing the child
// starts can leave the job, and the child starts suspended and runs only once
// it is inside: a limit applies to allocations made after the assignment, and
// a child let run first could commit its whole working set before it was held.
// taskkill /T misses an MSYS grandchild; the job holds every process in it.
type jobTree struct {
	mu  sync.Mutex
	job windows.Handle
}

// pidTree guards a light child by walking the parent-child tree from its pid.
type pidTree struct{ pid int }

func prepare(cmd *exec.Cmd, heavy bool, memoryMB int64) (tree, error) {
	if !heavy {
		return &pidTree{}, nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if memoryMB > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(memoryMB) << 20
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job) // the job was never used
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	return &jobTree{job: job}, nil
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

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
		return errors.New("run: could not resume the child after it joined its job")
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
	if j.job != 0 {
		_ = windows.TerminateJobObject(j.job, 1) // as in kill
		_ = windows.CloseHandle(j.job)           // closing kills on close even where the terminate was refused
		j.job = 0
	}
}

func (p *pidTree) attach(child *os.Process) error {
	p.pid = child.Pid
	return nil
}

func (p *pidTree) kill() {
	_ = proc.KillTree(p.pid) // taskkill says so when the tree is already gone
}

// finish leaves a light child's descendants alone, as a light child's own
// exit has always done.
func (p *pidTree) finish() {}
