//go:build windows

package tdd

import "syscall"

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
