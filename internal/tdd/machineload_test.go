package tdd

import (
	"strings"
	"testing"
)

// ratchet: test_removed TestForeignProcs_IncludesANonDescendant: superseded
// by TestForeignProcs_IncludesAFullyResolvedForeignChain below — its old
// synthetic data (an unrelated pid with an unresolvable one-hop ancestry)
// stopped proving inclusion once classifyAncestry started requiring a fully
// validated chain, and now proves the opposite (exclusion) instead. Not a
// coverage loss: the replacement asserts the same "inclusion still works"
// claim under the stricter, creation-time-aware classifier (#548 cold
// review, RED 1).
// ratchet: test_removed TestIsSelfOrDescendant_BoundedAgainstCyclicParents: renamed to
// TestClassifyAncestry_BoundedAgainstCyclicParents below, testing the same depth-bound
// safety property against classifyAncestry (isSelfOrDescendant no longer exists — the
// ancestry walk gained a third answer, unknown, that a bare bool could not carry).

// TestForeignProcs_ExcludesADescendantOfSelf pins the direction the issue
// warns is easiest to get backwards (#526): a process this gate invocation
// spawned, however many generations down, must never show up as "foreign
// load" — reporting the gate's own work as an outside culprit is worse than
// reporting nothing. Every pid here has a fully live, validly-timed chain,
// so this exercises the ancestrySelfOrDescendant path specifically, not the
// unknown/broken-chain one the tests below cover.
func TestForeignProcs_ExcludesADescendantOfSelf(t *testing.T) {
	const self = 100
	all := []procSample{
		{pid: self, ppid: 1, name: "aphrollo.exe", creation: 10},
		{pid: 200, ppid: self, name: "go.exe", creation: 20},     // direct child
		{pid: 300, ppid: 200, name: "go_test.exe", creation: 30}, // grandchild
		{pid: 999, ppid: 1, name: "find.exe", creation: 5},       // unrelated, no valid chain given
	}
	foreign, unattributed := classifyAll(self, all)
	for _, p := range append(append([]procSample{}, foreign...), unattributed...) {
		if p.pid == self || p.pid == 200 || p.pid == 300 {
			t.Errorf("classifyAll(%d, ...) placed pid %d (%s), a descendant of self, in foreign=%+v or unattributed=%+v", self, p.pid, p.name, foreign, unattributed)
		}
	}
}

// foreignChainSample builds a fully-resolved, definitely-foreign process at
// pid leaf: a synthetic ancestor chain long enough to satisfy
// classifyAncestry's own bound (maxAncestryDepth) with every hop valid and
// strictly earlier-created going up, never touching any pid this package's
// tests use as self. Shared with the integration tests in
// timeout_reject_test.go, which run against the REAL test process's own pid
// and so cannot risk a chain that merely looks foreign without being
// provably so under the stricter, creation-time-aware classifier (#548 cold
// review, RED 1).
func foreignChainSample(leaf int, name string, pctOneCore, cpuHours float64) []procSample {
	samples := []procSample{{
		pid: leaf, ppid: leaf + 1, name: name,
		pctOneCore: pctOneCore, cpuHours: cpuHours, creation: 1_000_000,
	}}
	for i := 1; i <= maxAncestryDepth+1; i++ {
		samples = append(samples, procSample{
			pid:      leaf + i,
			ppid:     leaf + i + 1,
			name:     "stranger.exe",
			creation: uint64(1_000_000 - i),
		})
	}
	return samples
}

// maxAssignableOSPID is the ceiling under which every pid a live process can
// hold sits, on both platforms this gate runs on: Windows hands out
// multiples of four far below it, and Linux's pid_max cannot exceed 2^22.
// Nothing above it is ever a running process — which is the whole property a
// synthetic fixture needs from its pid space.
const maxAssignableOSPID = 1 << 22

// timeoutLoadFixtureLeaf and checkStageLoadFixtureLeaf are the leaves of the
// two chains that get classified against the REAL test process's own pid
// (foreignLoadReport passes os.Getpid()), so they are the two that must live
// above maxAssignableOSPID — see
// TestForeignChainSample_CannotBeClaimedByTheProcessRunningIt.
// Both sit a clear order of magnitude above that ceiling, so the chains they
// build (leaf + maxAncestryDepth + 1 pids) stay out of reach of any live
// process on any runner.
const (
	syntheticPIDBase          = 1 << 30
	timeoutLoadFixtureLeaf    = syntheticPIDBase + 999
	checkStageLoadFixtureLeaf = syntheticPIDBase + 42
)

// A chain built at a leaf inside the range an OS actually assigns is a
// fixture that can BE the process running the test. That is not theory: the
// windows CI runner executed the package at pid 1048, which sits inside the
// chain based at 999, so classifyAncestry resolved find.exe's parent walk to
// self, correctly dropped the whole leg as "self's own tree", and
// TestPrecommit_MechanicalTimeoutNamesTheBoxLoad saw a report naming three
// unattributed stranger.exe entries and no find.exe at all — red on that
// runner and nowhere else. The classifier is right; the fixture's pid space
// was wrong.
func TestForeignChainSample_CannotBeClaimedByTheProcessRunningIt(t *testing.T) {
	all := foreignChainSample(timeoutLoadFixtureLeaf, "find.exe", 90, 2)

	// The mechanism itself, shown rather than remembered: a self pid
	// anywhere in the chain costs the report its foreign leaf.
	collided := all[len(all)/2].pid
	foreign, _ := classifyAll(collided, all)
	for _, p := range foreign {
		if p.name == "find.exe" {
			t.Fatalf("classifyAll(%d, ...) still named find.exe foreign; this test's premise (a self pid inside the chain drops it) no longer holds", collided)
		}
	}

	// So no pid in either chain may be one an OS could hand a live process.
	for _, leaf := range []int{timeoutLoadFixtureLeaf, checkStageLoadFixtureLeaf} {
		for _, p := range foreignChainSample(leaf, "find.exe", 90, 2) {
			if p.pid <= maxAssignableOSPID {
				t.Fatalf("fixture pid %d (chain from leaf %d) is inside the range an OS assigns to live processes (<= %d): a runner handed that pid reads the chain as its own tree and the timeout report loses its foreign process",
					p.pid, leaf, maxAssignableOSPID)
			}
		}
	}
}

// TestForeignProcs_IncludesAFullyResolvedForeignChain is the other half of
// the same proof: a filter that excludes everything looks identical, in a
// green run, to one that correctly excludes only self's own tree.
func TestForeignProcs_IncludesAFullyResolvedForeignChain(t *testing.T) {
	const self = 100
	all := append([]procSample{{pid: self, ppid: 1, name: "aphrollo.exe", creation: 1}},
		foreignChainSample(9000, "stranger-leaf.exe", 42, 1)...)

	foreign, _ := classifyAll(self, all)
	found := false
	for _, p := range foreign {
		if p.pid == 9000 {
			found = true
		}
	}
	if !found {
		t.Errorf("classifyAll(%d, ...) foreign = %+v, want pid 9000 (a fully resolved, unrelated chain) included", self, foreign)
	}
}

// TestForeignProcs_ExcludesAGateDescendantWhoseImmediateParentExited pins
// the cold-review fix directly: Windows never reparents an orphan, so a
// legitimate gate descendant whose immediate parent already exited has a
// ppid pointing at a pid that is simply gone from this snapshot — pid 200
// (self's real child) is not in `all` at all. The old walk read "parent not
// found" as "not self, therefore foreign"; this pins that such a descendant
// is now UNKNOWN — never named foreign — but also never dropped silently:
// it must surface in unattributed, so a reader still sees the CPU it is
// spending even though nobody can prove whose it is (#548 cold review,
// round 2).
func TestForeignProcs_ExcludesAGateDescendantWhoseImmediateParentExited(t *testing.T) {
	const self = 100
	all := []procSample{
		{pid: self, ppid: 1, name: "aphrollo.exe", creation: 10},
		// pid 200 (self's direct child) already exited and is absent —
		// only its orphaned grandchild survives into this snapshot.
		{pid: 300, ppid: 200, name: "leftover-test.exe", creation: 20},
	}
	foreign, unattributed := classifyAll(self, all)
	for _, p := range foreign {
		if p.pid == 300 {
			t.Fatalf("classifyAll(%d, ...) wrongly named pid 300 (leftover-test.exe) foreign — its parent simply exited and it cannot be attributed either way, foreign=%+v", self, foreign)
		}
	}
	found := false
	for _, p := range unattributed {
		if p.pid == 300 {
			found = true
		}
	}
	if !found {
		t.Errorf("classifyAll(%d, ...) unattributed = %+v, want pid 300 present (unresolved, not dropped)", self, unattributed)
	}
}

// TestForeignProcs_ExcludesAGateDescendantBehindARecycledPid pins the other
// half: pid 200 (self's real child) exited, and the OS handed pid 200 to an
// unrelated process before this sample ran. A walk that trusts the ppid
// number alone would follow it into the stranger's ancestry and conclude
// "not self" — the creation-time check must catch that the new pid-200
// process was created AFTER the child (300) it supposedly parented, and
// refuse the hop rather than wander through it. Pid 200's OWN ancestry is
// made to resolve fully and cleanly (never touching self, never hitting a
// missing parent) specifically so that ONLY the creation-time check, not an
// incidental missing-parent break further up, is what can be excluding
// pid 300 — bypassing the creation check here must let the walk sail
// through into ancestryForeign, proving that check is load-bearing.
func TestForeignProcs_ExcludesAGateDescendantBehindARecycledPid(t *testing.T) {
	const self = 100
	all := []procSample{
		{pid: self, ppid: 1, name: "aphrollo.exe", creation: 10},
		// pid 300 is self's real grandchild, created while the ORIGINAL
		// pid-200 child was still alive.
		{pid: 300, ppid: 200, name: "leftover-test.exe", creation: 20},
		// pid 200 now belongs to an unrelated process, created AFTER 300 —
		// proof it cannot be the same process that parented 300.
		{pid: 200, ppid: 201, name: "unrelated.exe", creation: 30},
	}
	// pid 200's own ancestor chain: fully valid, never touching self.
	all = append(all, foreignChainSample(201, "stranger-ancestor.exe", 0, 0)...)

	foreign, unattributed := classifyAll(self, all)
	for _, p := range foreign {
		if p.pid == 300 {
			t.Fatalf("classifyAll(%d, ...) followed a recycled pid into a stranger's ancestry and wrongly named pid 300 foreign, foreign=%+v", self, foreign)
		}
	}
	found := false
	for _, p := range unattributed {
		if p.pid == 300 {
			found = true
		}
	}
	if !found {
		t.Errorf("classifyAll(%d, ...) unattributed = %+v, want pid 300 present (recycled parent, unresolved rather than dropped)", self, unattributed)
	}
}

// TestClassifyAncestry_BoundedAgainstCyclicParents guards the walk itself:
// a snapshot torn mid-read (or simply wrong) could hand back a parent chain
// that cycles. Sampling runs on a box already in trouble — an infinite loop
// here would hang the very rejection path it is meant to make more useful.
// No local timer wraps this call on purpose (the gate itself scans test
// files for real-time waits): a regression that removes the depth bound
// hangs this test, and the suite's own run budget is what catches it.
func TestClassifyAncestry_BoundedAgainstCyclicParents(t *testing.T) {
	// 1 and 2 are each other's "parent", both with equal, non-decreasing
	// creation times so the creation-time check alone cannot break the
	// cycle — only the depth bound can.
	byPID := map[int]procSample{
		1: {pid: 1, ppid: 2, creation: 10},
		2: {pid: 2, ppid: 1, creation: 10},
	}
	if got := classifyAncestry(999, 1, byPID); got == ancestrySelfOrDescendant {
		t.Errorf("classifyAncestry(999, 1, cyclic byPID) = %v, want anything but ancestrySelfOrDescendant — 999 never appears in the cycle", got)
	}
}

// TestFormatMachineLoad_RendersBoxLoadAndTopForeignProcesses pins the shape
// a reader sees: the box line always renders, and each foreign process gets
// its own "foreign load:" line naming image, pid, current share of one core,
// and cumulative CPU time.
func TestFormatMachineLoad_RendersBoxLoadAndTopForeignProcesses(t *testing.T) {
	foreign := []procSample{
		{pid: 21768, name: "find.exe", pctOneCore: 99, cpuHours: 4.5},
		{pid: 33356, name: "find.exe", pctOneCore: 99, cpuHours: 4.0},
	}
	got := formatMachineLoad(24, 61, foreign, nil)
	want := "box: 24 cores, load 61%\n" +
		"foreign load: find.exe (pid 21768) 99% of one core, 4.5h CPU\n" +
		"foreign load: find.exe (pid 33356) 99% of one core, 4.0h CPU"
	if got != want {
		t.Errorf("formatMachineLoad(...) =\n%q\nwant\n%q", got, want)
	}
}

// TestFormatMachineLoad_RendersUnattributedSeparatelyFromForeign pins the
// round-2 fix directly: a process the walk could not settle must still
// reach the reader, under its own "unattributed load:" heading, never
// merged into "foreign load:" and never silently dropped (#548 cold review,
// round 2 — reporting nothing here is not safer than the misattribution it
// replaced, only quieter about failing).
func TestFormatMachineLoad_RendersUnattributedSeparatelyFromForeign(t *testing.T) {
	foreign := []procSample{{pid: 9000, name: "stranger.exe", pctOneCore: 50, cpuHours: 1}}
	unattributed := []procSample{{pid: 300, name: "leftover-test.exe", pctOneCore: 90, cpuHours: 2.5}}
	got := formatMachineLoad(4, 55, foreign, unattributed)
	want := "box: 4 cores, load 55%\n" +
		"foreign load: stranger.exe (pid 9000) 50% of one core, 1.0h CPU\n" +
		"unattributed load: leftover-test.exe (pid 300) 90% of one core, 2.5h CPU"
	if got != want {
		t.Errorf("formatMachineLoad(...) =\n%q\nwant\n%q", got, want)
	}
}

// TestFormatMachineLoad_NoForeignProcessesStillRendersBox is the clean-box
// case: a box under 600s but with nothing foreign or unattributed running
// must still say so, not print a bare "box: ..." line with a trailing
// newline into nothing or an empty section that looks like a bug.
func TestFormatMachineLoad_NoForeignProcessesStillRendersBox(t *testing.T) {
	got := formatMachineLoad(8, 12, nil, nil)
	want := "box: 8 cores, load 12%"
	if got != want {
		t.Errorf("formatMachineLoad(8, 12, nil, nil) = %q, want %q", got, want)
	}
}

// TestTopByLoad_SortsDescendingAndCaps proves the "top few" selection: the
// highest current-CPU process leads, and the list never exceeds n even when
// more foreign processes were sampled.
func TestTopByLoad_SortsDescendingAndCaps(t *testing.T) {
	in := []procSample{
		{pid: 1, pctOneCore: 10},
		{pid: 2, pctOneCore: 90},
		{pid: 3, pctOneCore: 50},
		{pid: 4, pctOneCore: 99},
	}
	got := topByLoad(in, 2)
	if len(got) != 2 {
		t.Fatalf("topByLoad(in, 2) returned %d entries, want 2", len(got))
	}
	if got[0].pid != 4 || got[1].pid != 2 {
		t.Errorf("topByLoad(in, 2) = %+v, want pid 4 then pid 2 (descending by pctOneCore)", got)
	}
}

// TestForeignLoadReport_UnavailableWhenProbeFails proves the degrade path
// #526 requires: a probe that cannot answer must render "load unavailable"
// rather than a zeroed-out, misleadingly confident report.
func TestForeignLoadReport_UnavailableWhenProbeFails(t *testing.T) {
	prev := machineLoadSampleFn
	sampled := make(chan struct{})
	machineLoadSampleFn = func(<-chan struct{}) (int, float64, []procSample, bool) {
		close(sampled)
		return 0, 0, nil, false
	}
	t.Cleanup(func() { machineLoadSampleFn = prev })

	got := foreignLoadReport(1234)
	if !strings.Contains(got, "load unavailable") {
		t.Fatalf("foreignLoadReport(...) = %q, want it to say load unavailable when the probe fails", got)
	}

	// A returned "load unavailable" does not prove this test's own stub ever
	// ran: the budget-expiry path says "load unavailable" too, and on a box
	// loaded enough to miss foreignLoadBudget the call returns while its
	// sampling goroutine is still queued. Left there, this test's Cleanup
	// restores machineLoadSampleFn while that straggler is still in flight —
	// the shape that produced the -race report in #549/#552. Waiting for the
	// stub pins the assertion to the failing-probe path it is named for AND
	// keeps the goroutine inside the test that started it.
	//
	// The decline verdict is the one reply that means no goroutine of ours
	// exists (a straggler from an earlier test still holds machineLoadMu),
	// and it also contains "load unavailable" — caught here so it reports as
	// a failure with its message rather than hanging on the receive below.
	if strings.Contains(got, "already in progress") {
		t.Fatalf("foreignLoadReport(...) = %q — no sample of this test's own was started, so the failing-probe path went unexercised", got)
	}
	<-sampled
}

// TestForeignLoadReport_BoundedAgainstAHungProbe is the sampling-hangs case
// #526 calls out explicitly: a probe that never returns must not hang the
// rejection it is trying to make more useful. The stub blocks on stop
// itself (the way a real probe should) rather than looping forever, so the
// goroutine foreignLoadReport starts is cleanly released once the budget
// expires and closes stop — never leaked past this test, and never holding
// machineLoadMu for a later test to trip over.
func TestForeignLoadReport_BoundedAgainstAHungProbe(t *testing.T) {
	prev := machineLoadSampleFn
	unblocked := make(chan struct{})
	machineLoadSampleFn = func(stop <-chan struct{}) (int, float64, []procSample, bool) {
		<-stop
		close(unblocked)
		return 0, 0, nil, false
	}
	t.Cleanup(func() { machineLoadSampleFn = prev })

	got := foreignLoadReport(1234)
	if !strings.Contains(got, "load unavailable") {
		t.Errorf("foreignLoadReport(...) = %q, want load unavailable once the sampling budget expires", got)
	}

	// Proves stop was actually closed, not just that the OUTER call has its
	// own independent timeout: a regression that drops close(stop) would
	// leave the stub blocked on <-stop forever, never freeing machineLoadMu
	// for the next rejection — this line would hang, and the suite's own
	// run budget (not a local timer; see the cyclic-parents test above for
	// why none lives here) is what would catch it.
	<-unblocked
}

// TestForeignLoadReport_SecondSampleWhileFirstInFlightDeclines proves the
// single-flight guard #548's cold review asked for: a second rejection
// landing while one sample is still in progress must not stack a second
// full double-enumeration on an already-overloaded box — it declines
// immediately instead of waiting or starting its own probe.
func TestForeignLoadReport_SecondSampleWhileFirstInFlightDeclines(t *testing.T) {
	prev := machineLoadSampleFn
	started := make(chan struct{})
	release := make(chan struct{})
	machineLoadSampleFn = func(<-chan struct{}) (int, float64, []procSample, bool) {
		close(started)
		<-release
		return 1, 0, nil, true
	}
	t.Cleanup(func() { machineLoadSampleFn = prev })

	done := make(chan string, 1)
	go func() { done <- foreignLoadReport(1) }()
	<-started // the first sample now holds machineLoadMu

	second := foreignLoadReport(2)
	if !strings.Contains(second, "already in progress") {
		t.Errorf("foreignLoadReport(...) while a first sample is in flight = %q, want it to decline immediately", second)
	}

	close(release)
	<-done // let the first call finish so nothing leaks past this test
}
