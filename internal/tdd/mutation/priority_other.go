//go:build !windows

package mutation

import "syscall"

// lowerOwnPriority moves this process, and every process it starts after, to
// nice 10. Background work yields the CPU, and with no explicit I/O class the
// kernel derives the disk priority from the nice value too. A refusal leaves
// the build at normal priority, which is no worse than before.
func lowerOwnPriority() {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10) // best effort, see above
}
