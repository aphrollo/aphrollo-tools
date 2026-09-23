//go:build !windows

package tdd

import (
	"syscall"
)

// suiteAttrs puts a SUITE child in its own process group, which is what makes
// proc.KillTree's negative-pid signal reach the launcher's children rather
// than only the launcher. It is deliberately NOT Setsid: the suite is a foreground
// child whose output the gate reads, not a detached phase.
func suiteAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
