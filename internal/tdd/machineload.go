package tdd

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// procSample is one process this box currently has running, as machine-load
// sampling sees it: an identity triple (pid, its parent, and the image
// name) plus how much CPU it is using. pctOneCore is the CURRENT share of
// one core over the sampling window (so a single-threaded runaway pinned to
// one core reads "99%", not "4%" of a 24-core box); cpuHours is the
// process's cumulative CPU time since it started, the number that tells a
// reader "how long has this been running" rather than just "is it busy
// right now".
type procSample struct {
	pid        int
	ppid       int
	name       string
	pctOneCore float64
	cpuHours   float64
}

// maxAncestryDepth bounds isSelfOrDescendant's walk up a parent chain. A
// live process tree is never this deep; the bound exists only so a torn or
// malformed snapshot (one that hands back a cycle) cannot hang the walk —
// this runs on a box already in trouble, where a hang is strictly worse
// than the bare timeout verdict it is trying to explain.
const maxAncestryDepth = 64

// isSelfOrDescendant reports whether pid IS self, or is reachable from self
// by following ParentProcessID links down the tree — i.e. self started it,
// directly or through some chain of children. parents maps a pid to its own
// parent pid, built from one process snapshot; a pid absent from the map
// (already exited, or never seen) ends the walk with "no".
func isSelfOrDescendant(self, pid int, parents map[int]int) bool {
	if pid == self {
		return true
	}
	for depth := 0; depth < maxAncestryDepth; depth++ {
		parent, ok := parents[pid]
		if !ok {
			return false
		}
		if parent == self {
			return true
		}
		pid = parent
	}
	return false
}

// foreignProcs returns every sample from all that is neither self nor
// spawned by self, directly or transitively, in the same order given. This
// is the one piece of #526 most likely to be silently wrong: get the
// direction backwards and the gate reports its own test processes as
// "foreign load", which actively misdirects the reader rather than saying
// nothing.
func foreignProcs(self int, all []procSample) []procSample {
	parents := make(map[int]int, len(all))
	for _, p := range all {
		parents[p.pid] = p.ppid
	}
	out := make([]procSample, 0, len(all))
	for _, p := range all {
		if !isSelfOrDescendant(self, p.pid, parents) {
			out = append(out, p)
		}
	}
	return out
}

// topByLoad returns the n samples with the highest pctOneCore, descending,
// leaving in untouched. n or fewer entries come back when in is shorter.
func topByLoad(in []procSample, n int) []procSample {
	sorted := append([]procSample(nil), in...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].pctOneCore > sorted[j].pctOneCore })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// formatMachineLoad renders the box-load line and one "foreign load:" line
// per entry in top, in the order given (the caller sorts). No trailing
// newline: every call site is interpolating this into a larger message.
func formatMachineLoad(cores int, loadPct float64, top []procSample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "box: %d cores, load %.0f%%", cores, loadPct)
	for _, p := range top {
		fmt.Fprintf(&b, "\nforeign load: %s (pid %d) %.0f%% of one core, %.1fh CPU", p.name, p.pid, p.pctOneCore, p.cpuHours)
	}
	return b.String()
}

// foreignLoadTopN is "the top few" processes #526 asks the report to name.
const foreignLoadTopN = 3

// foreignLoadBudget bounds how long a timeout rejection may spend sampling
// the machine before giving up. It runs on a box already shown to be in
// trouble (that is the whole reason it fires): a rejection that fails to
// render because sampling hung would be strictly worse than the bare
// verdict it is trying to improve on.
const foreignLoadBudget = 2 * time.Second

// machineLoadSampleFn is the OS probe seam: overall CPU load, core count,
// and one sample per live process, ok=false when the probe could not
// answer (or is not implemented on this OS). Swapped in tests.
var machineLoadSampleFn = machineLoadSample

// foreignLoadReport samples the machine ONCE — overall CPU load plus the
// top few processes by current CPU that are neither this gate invocation
// nor something it spawned — and renders it for a timeout rejection.
// Bounded by foreignLoadBudget and read off a buffered channel so a probe
// that never returns cannot hang the caller: the goroutine it started is
// leaked, not blocked on, and the buffered send lets it exit whenever it
// eventually does.
func foreignLoadReport(selfPID int) string {
	type sample struct {
		cores   int
		loadPct float64
		procs   []procSample
		ok      bool
	}
	ch := make(chan sample, 1)
	go func() {
		cores, loadPct, procs, ok := machineLoadSampleFn()
		ch <- sample{cores, loadPct, procs, ok}
	}()
	select {
	case s := <-ch:
		if !s.ok {
			return "box: load unavailable"
		}
		top := topByLoad(foreignProcs(selfPID, s.procs), foreignLoadTopN)
		return formatMachineLoad(s.cores, s.loadPct, top)
	case <-time.After(foreignLoadBudget):
		return "box: load unavailable (sampling exceeded its own budget)"
	}
}
