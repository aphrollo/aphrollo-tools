//go:build !windows

// Package proc holds the one process-lifetime primitive every spawn site in
// this repo needs: ending a child AND everything it started. It lives on its
// own so internal/tdd (build phases, suites, mutation runs) and
// internal/refactor (language servers, which spawn cargo underneath
// themselves) share ONE implementation of a fact that is entirely
// per-platform.
package proc

import (
	"strconv"
	"syscall"
)

// killTreePlan describes the kill for a test and a reader: a NEGATIVE pid is
// the process group, which is why TreeAttrs puts each child in its own —
// killing the parent alone left its cargo children running.
func killTreePlan(pid int) []string {
	return []string{"kill", "-KILL", "-" + strconv.Itoa(pid)}
}

// KillTree signals the child's whole process group. Because this is a
// negative-pid group signal, a cmd it is used on must have been started with
// a group-creating SysProcAttr (TreeAttrs, or internal/tdd's suiteAttrs /
// detachedAttrs) — otherwise the signal misses every descendant of a child
// that never became a group leader, and a child that IS still in the
// caller's own group would take the caller down with it.
func KillTree(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

// TreeAttrs is what a child must be started with for KillTree to reach ITS
// descendants: its own process group, which the child's own children then
// inherit. Deliberately NOT Setsid — this is for a foreground child whose
// pipes the caller reads, not a detached phase.
func TreeAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
