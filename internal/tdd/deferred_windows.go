//go:build windows

package tdd

import (
	"os/exec"
	"strconv"
	"syscall"
)

// detachedAttrs makes a spawned phase survive the hook's exit. DETACHED_PROCESS
// gives it no console (a hook has none to inherit anyway) and
// CREATE_NEW_PROCESS_GROUP keeps a Ctrl-C aimed at the session from reaching
// it — without both, killing the hook's console takes the build with it.
func detachedAttrs() *syscall.SysProcAttr {
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
	)
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
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
