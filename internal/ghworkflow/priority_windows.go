//go:build windows

// twin: internal/ghworkflow/priority_unix.go
package ghworkflow

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// Every step runs below normal priority, so a local CI run on a box someone
// else is using takes what is left of it. A process inherits its parent's
// priority class, so what the step starts runs there too.

// lowPriorityArgv is argv unchanged: on Windows the priority is a creation flag.
func lowPriorityArgv(argv []string) []string { return argv }

// lowPriorityAttrs adds BELOW_NORMAL_PRIORITY_CLASS to a step's process attributes.
func lowPriorityAttrs(attrs *syscall.SysProcAttr) *syscall.SysProcAttr {
	if attrs == nil {
		attrs = &syscall.SysProcAttr{}
	}
	attrs.CreationFlags |= windows.BELOW_NORMAL_PRIORITY_CLASS
	return attrs
}

// priorityNote says what the steps run under.
func priorityNote() string { return "steps run at BELOW_NORMAL_PRIORITY_CLASS" }
