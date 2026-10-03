//go:build linux

package run

import "syscall"

// dieWithParent has the kernel kill the child when the process that started
// it dies, however it dies: a SIGKILL, a crash or an out-of-memory kill no
// handler of ours sees. It reaches the child itself, not what the child
// started, which a signal this process could catch (forward.go) does reach.
//
// Pdeathsig fires when the thread that started the child exits, not the
// process. That holds here for the whole life of the process: start does not
// lock its goroutine to a thread, and the Go runtime ends a thread only when a
// goroutine locked to it exits, so the thread that forked the child lives as
// long as this process does. A caller that locks its own goroutine to a thread
// and lets that goroutine return without unlocking would end its thread and
// the children it started with it.
func dieWithParent(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
