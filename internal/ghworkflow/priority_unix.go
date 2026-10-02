//go:build !windows

package ghworkflow

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Every step runs below normal priority, so a local CI run on a box someone
// else is using takes what is left of it: nice for the CPU and, where the host
// has it and allows it, ionice's lowest best-effort class for the disk.

// priorityWrappers are the commands that put what follows them at low
// priority, outermost first. ionice is only used when a trial run of it works:
// in a container that bars the syscall it refuses and would run nothing.
var priorityWrappers = sync.OnceValue(func() [][]string {
	var wrappers [][]string
	if p, err := exec.LookPath("ionice"); err == nil && exec.Command(p, "-c", "2", "-n", "7", "true").Run() == nil {
		wrappers = append(wrappers, []string{p, "-c", "2", "-n", "7"})
	}
	if p, err := exec.LookPath("nice"); err == nil {
		wrappers = append(wrappers, []string{p, "-n", "10"})
	}
	return wrappers
})

// lowPriorityArgv is argv run under nice and ionice.
func lowPriorityArgv(argv []string) []string {
	var out []string
	for _, w := range priorityWrappers() {
		out = append(out, w...)
	}
	return append(out, argv...)
}

// lowPriorityAttrs is the process attributes a step starts with: unchanged
// here, since the priority is in the command.
func lowPriorityAttrs(attrs *syscall.SysProcAttr) *syscall.SysProcAttr { return attrs }

// priorityNote says what the steps run under.
func priorityNote() string {
	wrappers := priorityWrappers()
	if len(wrappers) == 0 {
		return "no nice or ionice on PATH, so steps run at normal priority"
	}
	var parts []string
	for _, w := range wrappers {
		parts = append(parts, filepath.Base(w[0])+" "+strings.Join(w[1:], " "))
	}
	return "steps run under " + strings.Join(parts, ", then ")
}
