package lock

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestWatchdogVerdict_InsideTheCapIsQuiet(t *testing.T) {
	procs := []procRSS{{PID: 10, PGRP: 10, Bytes: 400 << 20}, {PID: 11, PGRP: 10, Bytes: 500 << 20}}
	total, victims, whole := watchdogVerdict(procs, 10, 1000<<20, false)
	if total != 900<<20 || len(victims) != 0 || whole {
		t.Fatalf("got (%d, %v, %v), want 900MB total and no kill under a 1000MB cap", total, victims, whole)
	}
}

func TestWatchdogVerdict_ExactlyTheCapIsInside(t *testing.T) {
	procs := []procRSS{{PID: 10, PGRP: 10, Bytes: 1000 << 20}}
	if _, victims, whole := watchdogVerdict(procs, 10, 1000<<20, false); len(victims) != 0 || whole {
		t.Fatal("a group exactly at the cap must not be killed: the cap is what it MAY hold")
	}
}

func TestWatchdogVerdict_OtherGroupsAreNeverCounted(t *testing.T) {
	procs := []procRSS{{PID: 10, PGRP: 10, Bytes: 100 << 20}, {PID: 99, PGRP: 99, Bytes: 30 << 30}}
	total, _, whole := watchdogVerdict(procs, 10, 1000<<20, false)
	if total != 100<<20 || whole {
		t.Fatalf("got total %d whole %v, want a neighbour's 30GB left out of this run's account", total, whole)
	}
}

func TestWatchdogVerdict_SuiteIsEndedWhole(t *testing.T) {
	procs := []procRSS{{PID: 10, PGRP: 10, Bytes: 600 << 20}, {PID: 11, PGRP: 10, Bytes: 600 << 20}}
	_, victims, whole := watchdogVerdict(procs, 10, 1000<<20, false)
	if !whole || len(victims) != 0 {
		t.Fatalf("got (%v, %v), want the whole tree ended over the cap", victims, whole)
	}
}

func TestWatchdogVerdict_MutationRunLosesOnlyTheLargestProcess(t *testing.T) {
	procs := []procRSS{{PID: 10, PGRP: 10, Bytes: 100 << 20}, {PID: 11, PGRP: 10, Bytes: 900 << 20}, {PID: 12, PGRP: 10, Bytes: 300 << 20}}
	_, victims, whole := watchdogVerdict(procs, 10, 1000<<20, true)
	if whole || !slices.Equal(victims, []int{11}) {
		t.Fatalf("got (%v, %v), want only pid 11, the runaway, ended", victims, whole)
	}
}

// fakeProbe records what a monitor did.
type fakeProbe struct {
	oom        func() (int, bool)
	procs      []procRSS
	treeKills  int
	killedPIDs []int
}

func (f *fakeProbe) probe(cgroup bool) capProbe {
	p := capProbe{
		procs:    func() []procRSS { return f.procs },
		killTree: func() { f.treeKills++ },
		killPIDs: func(pids []int) { f.killedPIDs = append(f.killedPIDs, pids...) },
	}
	if cgroup {
		p.oomKills = f.oom
	}
	return p
}

func TestCapMonitor_CgroupKillEndsASuiteAndReportsIt(t *testing.T) {
	f := &fakeProbe{oom: func() (int, bool) { return 1, true }}
	m := &capMonitor{cap: MemCap{MB: 1000}, mode: modeCgroup, probe: f.probe(true)}
	m.poll()
	m.poll()
	res := m.result()
	if !res.Killed || res.Kills != 1 || f.treeKills != 1 {
		t.Fatalf("result %+v, tree kills %d: want Killed, 1 kill, the tree ended exactly once", res, f.treeKills)
	}
}

func TestCapMonitor_CgroupKillInAMutationRunIsCountedNotFatal(t *testing.T) {
	f := &fakeProbe{oom: func() (int, bool) { return 3, true }}
	m := &capMonitor{cap: MemCap{MB: 1000, KillLargest: true}, mode: modeCgroup, probe: f.probe(true)}
	m.poll()
	res := m.result()
	if res.Killed || res.Kills != 3 || f.treeKills != 0 {
		t.Fatalf("result %+v, tree kills %d: want 3 workers killed by the cap and the run left alone", res, f.treeKills)
	}
}

func TestCapMonitor_UnreadableScopeFallsBackToTheWatchdog(t *testing.T) {
	f := &fakeProbe{
		oom:   func() (int, bool) { return 0, false },
		procs: []procRSS{{PID: 5, PGRP: 5, Bytes: 2000 << 20}},
	}
	m := &capMonitor{cap: MemCap{MB: 1000}, mode: modeCgroup, pgrp: 5, probe: f.probe(true)}
	m.poll()
	if res := m.result(); !res.Killed || f.treeKills != 1 {
		t.Fatalf("result %+v: an unreadable cgroup must not leave a run uncapped", res)
	}
}

func TestCapMonitor_ReadableScopeIsNotSecondGuessedByTheWatchdog(t *testing.T) {
	f := &fakeProbe{
		oom:   func() (int, bool) { return 0, true },
		procs: []procRSS{{PID: 5, PGRP: 5, Bytes: 2000 << 20}},
	}
	m := &capMonitor{cap: MemCap{MB: 1000}, mode: modeCgroup, pgrp: 5, probe: f.probe(true)}
	m.poll()
	if res := m.result(); res.Killed || f.treeKills != 0 {
		t.Fatalf("result %+v: with the kernel enforcing, RSS above the cap (shared pages counted twice) is not a kill", res)
	}
}

func TestCapMonitor_WatchdogEndsTheTreeOnceAndCountsTheLargest(t *testing.T) {
	f := &fakeProbe{procs: []procRSS{{PID: 5, PGRP: 5, Bytes: 600 << 20}, {PID: 6, PGRP: 5, Bytes: 700 << 20}}}
	m := &capMonitor{cap: MemCap{MB: 1000}, mode: modeWatchdog, pgrp: 5, probe: f.probe(false)}
	m.poll()
	m.poll()
	if res := m.result(); !res.Killed || res.Kills != 1 || f.treeKills != 1 {
		t.Fatalf("result %+v tree kills %d: want one whole-tree kill however often it looks", res, f.treeKills)
	}
}

func TestCapMonitor_WatchdogMutationRunKillsTheRunawayAndKeepsGoing(t *testing.T) {
	f := &fakeProbe{procs: []procRSS{{PID: 5, PGRP: 5, Bytes: 100 << 20}, {PID: 6, PGRP: 5, Bytes: 1500 << 20}}}
	m := &capMonitor{cap: MemCap{MB: 1000, KillLargest: true}, mode: modeWatchdog, pgrp: 5, probe: f.probe(false)}
	m.poll()
	res := m.result()
	if res.Killed || res.Kills != 1 || !slices.Equal(f.killedPIDs, []int{6}) || f.treeKills != 0 {
		t.Fatalf("result %+v killed %v tree %d: want pid 6 ended and the run alive", res, f.killedPIDs, f.treeKills)
	}
}

func TestCapResult_LineNamesTheCapNotATimeout(t *testing.T) {
	line := CapResult{Killed: true, Cap: MemCap{MB: 11911}}.Line()
	if line != "OOM-KILLED at 11.6 GB" {
		t.Fatalf("line = %q, want %q", line, "OOM-KILLED at 11.6 GB")
	}
	if strings.Contains(strings.ToLower(line), "time") {
		t.Fatalf("line %q must never read as a timeout", line)
	}
}

func TestRunCapped_ZeroCapNeverReachesTheLauncher(t *testing.T) {
	called := false
	prev := capLauncherFn
	capLauncherFn = func(*exec.Cmd, MemCap) (CapResult, error) { called = true; return CapResult{}, nil }
	t.Cleanup(func() { capLauncherFn = prev })
	res, err := RunCapped(exec.Command(os.Args[0], "-test.run=^$"), MemCap{})
	if err != nil || called || res.Mode != "none" {
		t.Fatalf("err=%v launcher called=%v mode=%q: an uncapped run must just run", err, called, res.Mode)
	}
}

func TestRunCapped_ACapGoesThroughTheLauncherAndItsAnswerIsReturned(t *testing.T) {
	prev := capLauncherFn
	capLauncherFn = func(_ *exec.Cmd, c MemCap) (CapResult, error) { return CapResult{Killed: true, Cap: c}, nil }
	t.Cleanup(func() { capLauncherFn = prev })
	res, err := RunCapped(exec.Command(os.Args[0], "-test.run=^$"), MemCap{MB: 512})
	if err != nil || !res.Killed || res.Cap.MB != 512 {
		t.Fatalf("res=%+v err=%v, want the launcher's kill passed through", res, err)
	}
}

// A kill that happened and finished between two polls is only visible in the
// count the kernel keeps: settle folds it in, and a suite's run is over the
// moment the cap ended anything in it.
func TestCapMonitor_SettleCountsAKillNoPollSaw(t *testing.T) {
	suite := &capMonitor{cap: MemCap{MB: 1000}, mode: modeCgroup}
	suite.settle(2)
	if res := suite.result(); !res.Killed || res.Kills != 2 {
		t.Fatalf("suite result %+v, want Killed with 2 kills", res)
	}
	mutation := &capMonitor{cap: MemCap{MB: 1000, KillLargest: true}, mode: modeCgroup}
	mutation.settle(2)
	if res := mutation.result(); res.Killed || res.Kills != 2 {
		t.Fatalf("mutation result %+v, want 2 workers killed and the run not ended", res)
	}
	clean := &capMonitor{cap: MemCap{MB: 1000}, mode: modeCgroup}
	clean.settle(0)
	if res := clean.result(); res.Killed || res.Kills != 0 {
		t.Fatalf("clean result %+v, want nothing", res)
	}
}

// Two processes holding the same amount tie for largest: the first one seen
// is the runaway named, never the last.
func TestWatchdogVerdict_ATieForLargestNamesTheFirstProcess(t *testing.T) {
	procs := []procRSS{{PID: 10, PGRP: 10, Bytes: 600 << 20}, {PID: 11, PGRP: 10, Bytes: 600 << 20}}
	_, victims, _ := watchdogVerdict(procs, 10, 1000<<20, true)
	if !slices.Equal(victims, []int{10}) {
		t.Fatalf("victims = %v, want pid 10, the first of the two equal processes", victims)
	}
}
