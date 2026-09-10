//go:build windows

// twin: internal/tdd/deferred_unix.go
package tdd

import (
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// detachedAttrs makes a spawned phase survive the hook's exit without putting
// a window on the user's desktop.
//
// CREATE_NO_WINDOW, deliberately NOT DetachedProcess: a detached child has no
// console at all, so the first console program it starts — cargo, and then
// every rustc — makes Windows allocate a NEW one and SHOW it. The wrapper is
// spawned on every edit in every session, so that produced hundreds of
// terminal windows. CREATE_NO_WINDOW gives the wrapper a console that is
// never shown and that its children inherit, so the whole build tree stays
// invisible. The wrapper's own stdio is redirected at the spawn site, so
// nothing writes to that console either.
//
// CREATE_NEW_PROCESS_GROUP stays: it keeps a Ctrl-C aimed at the session from
// reaching the build, and it is what makes proc.KillTree's taskkill /T the
// only thing that ends a phase.
func detachedAttrs() *syscall.SysProcAttr {
	const (
		createNewProcessGroup = 0x00000200
		createNoWindow        = 0x08000000
	)
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow}
}

// processStartTime asks Windows for the creation time it stamped on pid at
// launch — the OS's own record, not anything this binary wrote — so
// pidStillOurs can tell a live process from whatever the OS handed the same
// pid to after ours exited. False means the pid names no process right now.
func processStartTime(pid int) (time.Time, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var creation, exitTime, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exitTime, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, creation.Nanoseconds()), true
}

// suiteAttrs is what a SUITE child is spawned with. Unlike a deferred phase it
// keeps the session's console — the suite's output is read through pipes, and
// CREATE_NO_WINDOW here would give the child no inheritable console, so every
// console program below it would allocate a fresh visible one. taskkill /T
// walks the parent-child tree and needs no process group, so nothing else is
// required for the cancel to reach the whole tree.
func suiteAttrs() *syscall.SysProcAttr { return nil }
