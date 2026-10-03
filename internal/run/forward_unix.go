//go:build !windows

package run

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// liveGroups is every heavy child group this process started and has not yet
// ended. It is armed for SIGINT and SIGTERM while one is live.
var liveGroups = newForwarder(forwardSeams{
	notify:  func(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM) },
	stop:    signal.Stop,
	send:    signalGroup,
	reraise: raiseAgain,
})

var errNotASignal = errors.New("run: not an OS signal")

// signalGroup delivers sig to the process group the child at pid leads: the
// negative pid is the group, which TreeAttrs gave the child.
func signalGroup(pid int, sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return errNotASignal
	}
	return syscall.Kill(-pid, s)
}

// raiseAgain delivers sig to this process.
func raiseAgain(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(os.Getpid(), s)
	}
}
