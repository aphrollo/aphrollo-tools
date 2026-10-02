package lock

import (
	"os/exec"
	"sync"
)

// The enforcement half of the memory cap: how a run started under a MemCap is
// held to it, and how a run the cap ended is told from a run that failed.
//
// Three enforcers, one contract (CapResult):
//
//	cgroup    Linux with a reachable user systemd manager that delegates the
//	          memory controller: the child runs in a transient scope with
//	          MemoryMax and no swap, so the KERNEL ends whatever crosses the
//	          line and only that scope's processes are ever candidates.
//	watchdog  any other unix: a goroutine sums the resident memory of the
//	          child's process group and ends it at the cap. Sampling, so a
//	          fast allocator can overshoot by a poll's worth; it is the
//	          fallback, not the wall.
//	job       Windows: a job object with a job memory limit, so an allocation
//	          past it is refused by the OS.
//
// launchCapped is the seam: the tests replace it to prove what a caller does
// with a kill, and one real-launch test exercises the platform's own path.

// The enforcer a CapResult names.
const (
	modeCgroup   = "cgroup"
	modeWatchdog = "watchdog"
)

// CapResult is what became of one capped run.
type CapResult struct {
	// Killed is true when the cap ended the whole run: nothing it printed or
	// exited with says anything about the code under test, so the caller
	// reports it INCONCLUSIVE, never red and never a timeout.
	Killed bool
	// Kills counts the processes the cap ended, which is more than one run's
	// worth for a mutation tool whose workers are killed one at a time.
	Kills int
	Cap   MemCap
	// Mode is the enforcer that held the run, "none" when it was uncapped.
	Mode string
}

// Line is the report for a Killed run: the one word the gate prints instead
// of a timeout.
func (r CapResult) Line() string {
	return "OOM-KILLED at " + r.Cap.Text()
}

// capLauncherFn starts cmd under the cap and waits for it.
var capLauncherFn = launchCapped

// RunCapped runs cmd to completion under c. The caller has set cmd's output
// (a buffer or a log), exactly as for cmd.Run; a zero cap runs it uncapped.
// The error is cmd.Run's own.
func RunCapped(cmd *exec.Cmd, c MemCap) (CapResult, error) {
	if c.MB <= 0 {
		return CapResult{Cap: c, Mode: "none"}, cmd.Run()
	}
	return capLauncherFn(cmd, c)
}

// RunSlotChild runs a build slot's child (a suite, a lint, a deferred phase)
// started in dir under the cap that dir's repo and this box give it.
func RunSlotChild(cmd *exec.Cmd, dir string) (CapResult, error) {
	return RunCapped(cmd, MemCapFor(dir, CapSlot))
}

// RunMutationChild runs a mutation tool's process tree started in dir under
// the pool-sized cap, ending only the runaway worker where it can. share is
// how many such trees run side by side (a measurement's shards), which split a
// derived pool between them.
func RunMutationChild(cmd *exec.Cmd, dir string, share int) (CapResult, error) {
	return RunCapped(cmd, MemCapFor(dir, CapMutation).splitAmong(share))
}

// procRSS is one process as the watchdog sees it.
type procRSS struct {
	PID   int
	PGRP  int
	Bytes int64
}

// watchdogVerdict judges one sample of the box's processes for one child
// group. total is the group's resident memory. victims is who to end, empty
// while the group is inside the cap: the single largest process when
// killLargest (a mutation run's runaway mutant, the run goes on), else nil
// with wholeTree set (a suite is ended whole).
func watchdogVerdict(procs []procRSS, pgrp int, capBytes int64, killLargest bool) (total int64, victims []int, wholeTree bool) {
	var largest procRSS
	for _, p := range procs {
		if p.PGRP != pgrp {
			continue
		}
		total += p.Bytes
		if p.Bytes > largest.Bytes {
			largest = p
		}
	}
	if total <= capBytes {
		return total, nil, false
	}
	if killLargest && largest.PID != 0 {
		return total, []int{largest.PID}, false
	}
	return total, nil, true
}

// capProbe is what a monitor reads and does, one struct so a test can drive
// a monitor with no process behind it.
type capProbe struct {
	// oomKills is the cgroup's own count of processes the kernel ended at the
	// cap; nil in watchdog mode. ok=false is unreadable, never zero, and
	// hands the look to procs.
	oomKills func() (int, bool)
	// procs samples the box: the watchdog's eyes, and the cgroup mode's
	// fallback while its scope cannot be read.
	procs func() []procRSS
	// killPIDs ends exactly these processes, killTree the whole child group.
	killPIDs func(pids []int)
	killTree func()
}

// capMonitor watches one child.
type capMonitor struct {
	probe capProbe
	cap   MemCap
	pgrp  int
	mode  string

	mu        sync.Mutex
	kills     int
	killed    bool
	lastKills int
}

// poll is one look. It is the whole decision, called by the loop and by the
// tests alike.
func (m *capMonitor) poll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.probe.oomKills != nil {
		if n, ok := m.probe.oomKills(); ok {
			if n > m.lastKills {
				m.lastKills, m.kills = n, n
				if !m.cap.KillLargest && !m.killed {
					m.killed = true
					m.probe.killTree()
				}
			}
			return
		}
		// The scope could not be read (yet, or at all): the watchdog below
		// enforces the same cap, so an unreadable cgroup is never an
		// uncapped run.
	}
	if m.probe.procs == nil {
		return
	}
	_, victims, whole := watchdogVerdict(m.probe.procs(), m.pgrp, m.cap.MB<<20, m.cap.KillLargest)
	switch {
	case whole && !m.killed:
		m.killed = true
		m.kills++
		m.probe.killTree()
	case len(victims) > 0:
		m.kills++
		m.probe.killPIDs(victims)
	}
}

func (m *capMonitor) result() CapResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return CapResult{Killed: m.killed, Kills: m.kills, Cap: m.cap, Mode: m.mode}
}

// settle folds in the kernel's final count of processes the cap ended,
// read once the run was over. It is authoritative where a poll can miss a
// kill that happened and finished between two looks: a cap that ended
// anything ended a suite's run, and only counted for a mutation run's.
func (m *capMonitor) settle(kills int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kills = max(m.kills, kills)
	if kills > 0 && !m.cap.KillLargest {
		m.killed = true
	}
}
