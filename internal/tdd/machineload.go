package tdd

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// procSample is one process this box currently has running, as machine-load
// sampling sees it: an identity triple (pid, its parent, and the image
// name), how much CPU it is using, and when it started. pctOneCore is the
// CURRENT share of one core over the sampling window (so a single-threaded
// runaway pinned to one core reads "99%", not "4%" of a 24-core box);
// cpuHours is the process's cumulative CPU time since it started. creation
// is its OS-reported creation time, in the same tick units the Windows
// probe reads it in (0 means unread/unreadable — see creationUnknown); it
// exists solely to defeat pid reuse when walking an ancestry chain
// (classifyAncestry), and plays no part in what gets PRINTED.
type procSample struct {
	pid        int
	ppid       int
	name       string
	pctOneCore float64
	cpuHours   float64
	creation   uint64
}

// creationUnknown is the sentinel a procSample's creation field carries when
// this probe could not read it — typically OpenProcess denied for a
// protected system process. A real Windows creation timestamp (100ns ticks
// since 1601) is never literally zero, so the sentinel never collides with a
// genuine reading.
const creationUnknown = 0

// maxAncestryDepth bounds classifyAncestry's walk up a parent chain. A live
// process tree is never this deep; the bound exists only so a torn or
// malformed snapshot (one that hands back a cycle) cannot hang the walk —
// this runs on a box already in trouble, where a hang is strictly worse than
// the bare timeout verdict it is trying to explain.
const maxAncestryDepth = 64

// ancestry is classifyAncestry's answer: definitively self or one of its
// descendants, definitively foreign (a fully validated chain that never
// touches self), or unknown — the chain broke before it could be resolved
// either way, and is reported as neither.
type ancestry int

const (
	ancestryUnknown ancestry = iota
	ancestrySelfOrDescendant
	ancestryForeign
)

// classifyAncestry walks pid's parent chain looking for self, validating
// every hop against creation time before trusting it. byPID is one process
// snapshot keyed by pid, built by the caller.
//
// Windows never reparents an orphan: when an intermediate ancestor exits,
// the survivor's ParentProcessID keeps pointing at a pid that is now either
// missing from this snapshot entirely, or has been handed by the OS to a
// completely unrelated process. A walk that trusted the ppid number alone
// would read the first case as "not self, therefore foreign" and the second
// as "wandered into a stranger's ancestry, therefore foreign" — both wrong
// whenever pid is actually self's own descendant behind a parent that
// happened to exit mid-run (cold review of #526's first version). Since a
// process's creation time can only ever be AT OR AFTER its real parent's, a
// candidate parent created LATER than the child it supposedly parented
// cannot be that parent — the pid was recycled, and the chain is broken
// exactly where the walk cannot tell descendant from stranger. Either kind
// of break returns ancestryUnknown, never ancestryForeign: an under-reported
// foreign list costs a reader one more step; a wrongly accused one sends
// them somewhere there is nothing to find.
func classifyAncestry(self, pid int, byPID map[int]procSample) ancestry {
	if pid == self {
		return ancestrySelfOrDescendant
	}
	current, ok := byPID[pid]
	if !ok || current.creation == creationUnknown {
		return ancestryUnknown
	}
	for depth := 0; depth < maxAncestryDepth; depth++ {
		if current.ppid == self {
			return ancestrySelfOrDescendant
		}
		parent, ok := byPID[current.ppid]
		if !ok {
			return ancestryUnknown // the declared parent already exited (or was reaped) between snapshots
		}
		if parent.creation == creationUnknown {
			return ancestryUnknown
		}
		if parent.creation > current.creation {
			return ancestryUnknown // this pid was recycled since it last parented anything upstream
		}
		current = parent
	}
	return ancestryForeign // walked the full bound on a fully validated chain, self never appeared
}

// foreignProcs returns every sample from all that classifyAncestry can
// CONFIRM is neither self nor spawned by self, directly or transitively, in
// the same order given. A sample this cannot confidently attribute either
// way (classifyAncestry's ancestryUnknown) is left out just like a
// descendant is: reporting it would risk exactly the misdirection this
// function exists to avoid.
func foreignProcs(self int, all []procSample) []procSample {
	byPID := make(map[int]procSample, len(all))
	for _, p := range all {
		byPID[p.pid] = p
	}
	out := make([]procSample, 0, len(all))
	for _, p := range all {
		if classifyAncestry(self, p.pid, byPID) == ancestryForeign {
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
// and one sample per live process, ok=false when the probe could not answer
// (or is not implemented on this OS). stop is closed when the caller has
// given up waiting — a real probe checks it between its two snapshots so an
// abandoned sample stops rather than finishing a full enumeration nobody
// will read. Swapped in tests.
var machineLoadSampleFn = machineLoadSample

// machineLoadMu is the single-flight guard: at most one sample is ever in
// progress. A second timeout rejection landing while one sample is still
// unwinding — most likely because the box is ALREADY overloaded, the same
// condition that made the first one miss its budget — must not stack a
// second full double-enumeration (CreateToolhelp32Snapshot plus
// OpenProcess/GetProcessTimes over every live process, twice) on top of the
// first; it declines immediately instead (cold review of #526's first
// version: an unbounded number of these can otherwise pile up, adding
// syscall and CPU load to exactly the machine this feature exists to
// diagnose).
var machineLoadMu sync.Mutex

// foreignLoadReport samples the machine ONCE — overall CPU load plus the
// top few processes by current CPU that are neither this gate invocation
// nor something it spawned — and renders it for a timeout rejection.
// Bounded by foreignLoadBudget and read off a buffered channel so a probe
// that never returns cannot hang the caller; the timeout path closes stop so
// an abandoned probe can notice and give up its own second enumeration, and
// machineLoadMu stays held (via the goroutine's own deferred Unlock) until
// it actually does, so a concurrent call sees the lock busy and declines
// rather than starting a second one.
func foreignLoadReport(selfPID int) string {
	if !machineLoadMu.TryLock() {
		return "box: load unavailable (a sample is already in progress)"
	}
	stop := make(chan struct{})
	type sample struct {
		cores   int
		loadPct float64
		procs   []procSample
		ok      bool
	}
	ch := make(chan sample, 1)
	go func() {
		defer machineLoadMu.Unlock()
		cores, loadPct, procs, ok := machineLoadSampleFn(stop)
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
		close(stop)
		return "box: load unavailable (sampling exceeded its own budget)"
	}
}
