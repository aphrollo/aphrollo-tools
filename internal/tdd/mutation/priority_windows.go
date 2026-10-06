//go:build windows

package mutation

import "golang.org/x/sys/windows"

// lowerOwnPriority moves this process to the below-normal priority class,
// which the processes it starts after inherit. A refusal leaves the build at
// normal priority, which is no worse than before.
func lowerOwnPriority() {
	_ = windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS) // best effort, see above
}
