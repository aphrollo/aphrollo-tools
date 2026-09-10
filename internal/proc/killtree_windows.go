//go:build windows

// twin: internal/proc/killtree_unix.go
package proc

import (
	"os/exec"
	"strconv"
	"syscall"
)

// killTreePlan is the command that ends a process AND everything it started:
// killing the parent alone left cargo and rustc compiling while the parent's
// death handed the target lock and the global slot to the next build.
func killTreePlan(pid int) []string {
	return []string{"taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)}
}

// KillTree runs that plan.
func KillTree(pid int) error {
	plan := killTreePlan(pid)
	return exec.Command(plan[0], plan[1:]...).Run()
}

// TreeAttrs is what a child must be started with for KillTree to reach ITS
// descendants. taskkill /T walks the parent-child tree itself, so Windows
// needs nothing extra — the unix twin's process group is the thing this
// answers for.
func TreeAttrs() *syscall.SysProcAttr { return nil }
