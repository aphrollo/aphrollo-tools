//go:build !windows

package lock

import (
	"os/exec"
	"time"
)

// The polling half of the watchdog and cgroup enforcers; the Windows job
// object enforces in the kernel and never polls.

// capPollEvery is how often a monitor looks: fast enough that a runaway
// allocating a gigabyte a second is caught within a fraction of one.
const capPollEvery = 100 * time.Millisecond

// run polls until stop closes, then looks once more: a kill in the last
// interval before the child exited must still be counted.
func (m *capMonitor) run(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(capPollEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			m.poll()
			return
		case <-t.C:
			m.poll()
		}
	}
}

// waitMonitored watches a started cmd until it exits, then asks final (nil
// when the enforcer has none) for the kernel's own count of kills.
func waitMonitored(cmd *exec.Cmd, m *capMonitor, final func() int) (CapResult, error) {
	stop, done := make(chan struct{}), make(chan struct{})
	go m.run(stop, done)
	err := cmd.Wait()
	close(stop)
	<-done
	if final != nil {
		m.settle(final())
	}
	return m.result(), err
}
