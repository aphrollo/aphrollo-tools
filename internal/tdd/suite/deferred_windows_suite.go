//go:build windows

package suite

import (
	"syscall"
)

// suiteAttrs is what a SUITE child is spawned with. Unlike a deferred phase it
// keeps the session's console — the suite's output is read through pipes, and
// CREATE_NO_WINDOW here would give the child no inheritable console, so every
// console program below it would allocate a fresh visible one. taskkill /T
// walks the parent-child tree and needs no process group, so nothing else is
// required for the cancel to reach the whole tree.
func suiteAttrs() *syscall.SysProcAttr { return nil }
