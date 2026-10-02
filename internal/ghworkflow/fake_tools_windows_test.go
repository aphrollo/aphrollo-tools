//go:build windows

// twin: internal/ghworkflow/fake_tools_unix_test.go
package ghworkflow

import "golang.org/x/sys/windows"

// probePriority says whether this process runs below normal priority.
func probePriority() string {
	class, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		return "unknown: " + err.Error()
	}
	if class == windows.BELOW_NORMAL_PRIORITY_CLASS || class == windows.IDLE_PRIORITY_CLASS {
		return "low"
	}
	return "normal"
}
