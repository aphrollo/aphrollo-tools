//go:build !windows && !linux

package run

import "syscall"

// dieWithParent does nothing where the kernel has no parent-death signal
// (darwin, the BSDs): there the forwarder carries a SIGINT or SIGTERM to the
// child's group, and a SIGKILL of this process leaves it running.
func dieWithParent(*syscall.SysProcAttr) {}
