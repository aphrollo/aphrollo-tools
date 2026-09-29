package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// A runaway test binary reached 26 GB of anonymous memory on a 31 GB box and
// the kernel killed whatever it found first, an interactive session included
// (issue #1005). Every process the gate starts therefore runs under a hard
// memory cap of its own, so a runaway kills only itself, and no run starts on
// a box that has no memory left to give it.
//
// This file is the decision half: what the cap is, what headroom a start
// needs, and how a reading of the box becomes either. Nothing here touches
// the machine except through memBoxFn, so every rule is provable against a
// hypothetical box. The launch half (memcap_run*.go) enforces the answer.

// MemBox is one reading of the box's memory, in megabytes. A zero field is
// UNKNOWN, never zero bytes: every rule below falls back to not constraining
// on an unknown, the rule mutantsMemoryTerm has always followed.
type MemBox struct {
	RAMMB       int64
	AvailMB     int64
	SwapTotalMB int64
	SwapFreeMB  int64
}

// memBoxFn is the box itself, a seam for the tests.
var memBoxFn = readMemBox

const (
	// memCapRAMPercent is the share of installed RAM every capped run may
	// divide between them: the rest is the box's own (the operator's shell,
	// the kernel, page cache). The cap is what one run may hold; the slot
	// count says how many run at once.
	memCapRAMPercent = 75
	// memCapFloorMB is the smallest cap a run is given: a cold Rust or Go
	// link needs a couple of gigabytes, and a cap below that kills honest
	// work.
	memCapFloorMB = 2048
	// memHeadroomFloorMB and memHeadroomCeilMB bound the derived headroom a
	// start needs.
	memHeadroomFloorMB = 2048
	memHeadroomCeilMB  = 8192
	// memSwapPressurePercent is the swap fill from which the box has no
	// room left to absorb a spike, and a start needs twice the headroom.
	memSwapPressurePercent = 90
	// memHeadroomPollEvery is how often a waiting start looks again.
	memHeadroomPollEvery = 5 * time.Second
)

// memCapKey and memHeadroomKey are the repo keys, both in [aphrollo].
const (
	memCapKey      = "memory-cap"
	memHeadroomKey = "memory-headroom"
)

// CapKind says what a cap is for: one slot's share of the box, or the whole
// pool for the one mutation tool the box-wide mutation lock lets run.
type CapKind int

const (
	// CapSlot is one build slot's run: a suite, a lint, a deferred phase.
	CapSlot CapKind = iota
	// CapMutation is a mutation tool's whole process tree. Its workers are
	// themselves mutants' test runs, so a kill at the cap lands on one
	// worker rather than on the run (see MemCap.KillLargest).
	CapMutation
)

// MemCap is the answer for one run.
type MemCap struct {
	// MB is the hard limit, 0 when this run is not capped.
	MB int64
	// Why is the arithmetic, for the log: which term bound the number, or
	// why there is no cap.
	Why string
	// Derived is true for a cap this file worked out, false for one a repo
	// declared: a repo's number is its word for one run and is never divided.
	Derived bool
	// KillLargest is the kill policy where the watchdog is the enforcer: end
	// only the biggest process, not the tree. A mutation run's runaway is
	// one mutant's test binary, and the run must go on measuring the rest.
	KillLargest bool
}

// Text is the cap as the report names it: `11.6 GB`.
func (c MemCap) Text() string {
	return fmt.Sprintf("%.1f GB", float64(c.MB)/1024)
}

// deriveMemCap is the default cap for one run on box, with slots build slots
// sharing it. The pool is the SMALLER of installed RAM's share and what is
// available right now — a busy box hands out less, never more, the rule
// mutantsBudgetMemoryGB set for the shard count — and a slot run gets
// pool/slots, a mutation run the whole pool.
//
// Unknown RAM is no cap at all: a number derived from a missing reading is a
// guess, and a guess that low kills honest work.
func deriveMemCap(box MemBox, slots int, kind CapKind) MemCap {
	if box.RAMMB <= 0 {
		return MemCap{Why: "memory unreadable, no cap"}
	}
	if slots < 1 {
		slots = 1
	}
	pool := box.RAMMB * memCapRAMPercent / 100
	term := fmt.Sprintf("ram %dMB*%d%%", box.RAMMB, memCapRAMPercent)
	if box.AvailMB > 0 && box.AvailMB < pool {
		pool = box.AvailMB
		term = fmt.Sprintf("free %dMB (measured)", box.AvailMB)
	}
	share, div := pool, 1
	if kind == CapSlot {
		share, div = pool/int64(slots), slots
	}
	why := fmt.Sprintf("%s/%d", term, div)
	if share < memCapFloorMB {
		share = memCapFloorMB
		why += fmt.Sprintf(" raised to the %dMB floor", memCapFloorMB)
	}
	return MemCap{MB: share, Why: why, Derived: true, KillLargest: kind == CapMutation}
}

// splitAmong is a derived pool cap divided between n runs that share it, the
// shards of one measurement: eight shards each allowed the whole pool are
// eight times the pool. A declared cap is not divided, and nothing goes below
// the floor.
func (c MemCap) splitAmong(n int) MemCap {
	if n < 2 || !c.Derived || c.MB <= 0 {
		return c
	}
	c.MB = max(c.MB/int64(n), memCapFloorMB)
	c.Why = fmt.Sprintf("%s, one of %d shards", c.Why, n)
	return c
}

// parseMemGB reads a `memory-cap` / `memory-headroom` value: a positive whole
// number of gigabytes, or `off` where the caller allows it.
func parseMemGB(key, raw string, allowOff bool) (gb int64, off bool, err error) {
	raw = strings.TrimSpace(raw)
	if allowOff && strings.EqualFold(raw, "off") {
		return 0, true, nil
	}
	n, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil || n < 1 {
		want := "a positive whole number of GB"
		if allowOff {
			want += " or off"
		}
		return 0, false, fmt.Errorf("%s must be %s, got %q", key, want, raw)
	}
	return n, false, nil
}

// MemCapFor is the cap for a run started in dir: the repo's `memory-cap`
// when it declares one, else the derived default. A declared value the
// reader cannot parse is refused loudly in Why and the derived default is
// used, never a silent no-cap: a repo that wrote a number believes it is
// obeyed, and a box left uncapped by a typo is the incident again.
func MemCapFor(dir string, kind CapKind) MemCap {
	if memCapOverride != nil {
		return *memCapOverride
	}
	derived := deriveMemCap(memBoxFn(), buildSlotCount(), kind)
	raw, set := repoAphrolloKey(dir, memCapKey)
	if !set {
		return derived
	}
	gb, off, err := parseMemGB(memCapKey, raw, true)
	switch {
	case err != nil:
		derived.Why = err.Error() + "; using the derived cap: " + derived.Why
		return derived
	case off:
		return MemCap{Why: memCapKey + " = off"}
	}
	return MemCap{MB: gb * 1024, Why: fmt.Sprintf("%s = %d in aphrollo.toml", memCapKey, gb), KillLargest: kind == CapMutation}
}

// memCapOverride replaces every derived and configured cap for one test.
var memCapOverride *MemCap

// SetMemCapForTest makes every run in this process take cap c and answers
// the restore. Exported because what a caller above this package does with a
// run the cap ended is only provable from that caller's package, and a real
// runaway needs a cap small enough for a test binary to cross.
func SetMemCapForTest(c MemCap) (restore func()) {
	prev := memCapOverride
	memCapOverride = &c
	return func() { memCapOverride = prev }
}

// SetMemBoxForTest pins the box's memory for one test and answers the
// restore, for the same reason: no test box is ever short of memory.
func SetMemBoxForTest(box MemBox) (restore func()) {
	prev := memBoxFn
	memBoxFn = func() MemBox { return box }
	return func() { memBoxFn = prev }
}

// deriveHeadroomMB is the available memory a start needs: an eighth of
// installed RAM, between 2 and 8 GB. Zero (unknown RAM) constrains nothing.
func deriveHeadroomMB(box MemBox) int64 {
	if box.RAMMB <= 0 {
		return 0
	}
	need := box.RAMMB / 8
	return min(max(need, memHeadroomFloorMB), memHeadroomCeilMB)
}

// swapPressured reports swap at or over memSwapPressurePercent full: the box
// has been paging for a while and has nothing left to absorb a spike.
func swapPressured(box MemBox) bool {
	if box.SwapTotalMB <= 0 {
		return false
	}
	used := box.SwapTotalMB - box.SwapFreeMB
	return used*100 >= box.SwapTotalMB*memSwapPressurePercent
}

// headroomVerdict judges one reading against the headroom a start needs
// (needMB, or the derived one when 0). ok with an empty reason is a start;
// otherwise reason is the one line a refusal or a wait prints.
func headroomVerdict(box MemBox, needMB int64) (ok bool, reason string) {
	if needMB <= 0 {
		needMB = deriveHeadroomMB(box)
	}
	if box.AvailMB <= 0 || needMB <= 0 {
		return true, "" // unreadable is not scarce
	}
	swap := ""
	if swapPressured(box) {
		needMB *= 2
		swap = fmt.Sprintf(", swap %d%% full so the requirement is doubled",
			(box.SwapTotalMB-box.SwapFreeMB)*100/box.SwapTotalMB)
	}
	if box.AvailMB >= needMB {
		return true, ""
	}
	return false, fmt.Sprintf("memory headroom: %.1f GB available, a start needs %.1f GB%s",
		float64(box.AvailMB)/1024, float64(needMB)/1024, swap)
}

// headroomSleepFn and headroomNowFn are the clock the wait keeps, seams for
// the tests.
var (
	headroomSleepFn = time.Sleep
	headroomNowFn   = time.Now
)

// WaitForHeadroom holds a start until the box has the memory it needs,
// polling every few seconds, and refuses once maxWait has passed. It answers
// the one line that says why: empty means start, non-empty is a refusal the
// caller reports as inconclusive — the same shape as a build slot that never
// came free. maxWait <= 0 asks once.
func WaitForHeadroom(dir string, maxWait time.Duration) string {
	need := int64(0)
	if raw, set := repoAphrolloKey(dir, memHeadroomKey); set {
		if gb, _, err := parseMemGB(memHeadroomKey, raw, false); err == nil {
			need = gb * 1024
		}
	}
	deadline := headroomNowFn().Add(maxWait)
	for {
		ok, reason := headroomVerdict(memBoxFn(), need)
		if ok {
			return ""
		}
		if !headroomNowFn().Add(memHeadroomPollEvery).Before(deadline) {
			return reason + fmt.Sprintf(" (waited %s)", maxWait.Round(time.Second))
		}
		headroomSleepFn(memHeadroomPollEvery)
	}
}

// repoAphrolloKey reads one key of the [aphrollo] table for the repo dir sits
// in: aphrollo.toml first, then Cargo.toml's [workspace.metadata.aphrollo],
// walking up from dir to the first directory that declares either.
func repoAphrolloKey(dir, key string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for range 12 {
		if v, ok := core.TomlStringIn(filepath.Join(dir, "aphrollo.toml"), "[aphrollo]", key); ok {
			return v, true
		}
		if v, ok := core.TomlStringIn(filepath.Join(dir, "Cargo.toml"), "[workspace.metadata.aphrollo]", key); ok {
			return v, true
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", false // the repo root, and it declares nothing
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}
