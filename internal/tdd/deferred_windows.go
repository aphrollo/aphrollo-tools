//go:build windows

package tdd

import (
	"os/exec"
	"strconv"
	"syscall"
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
// reaching the build, and it is what makes the taskkill /T below the only
// thing that ends a phase.
func detachedAttrs() *syscall.SysProcAttr {
	const (
		createNewProcessGroup = 0x00000200
		createNoWindow        = 0x08000000
	)
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow}
}

// killTreePlan is the command that ends a phase AND everything it started:
// killing the wrapper alone left cargo and rustc compiling while the
// wrapper's death handed the target lock and the global slot to the next
// build.
func killTreePlan(pid int) []string {
	return []string{"taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)}
}

// killTree runs that plan.
func killTree(pid int) error {
	plan := killTreePlan(pid)
	return exec.Command(plan[0], plan[1:]...).Run()
}
