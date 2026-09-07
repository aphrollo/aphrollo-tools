package tdd

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
)

// runFailFirstAndMechanicalConcurrently runs gateRoot's two suite stages —
// the fail-first RED proof (a throwaway worktree at HEAD, staged tests only
// applied) and the mechanical suite (the working tree, staged source AND
// tests) — IN PARALLEL rather than one after the other (issue #535). The two
// read different trees and neither writes state the other reads, so their
// sequencing was incidental.
//
// wg.Wait() below always waits for both, so there is no short-circuit: a
// fail-first `violated`/`vacuous` block — the one verdict the sequential
// version used to return on before mechanical's suite build ever started —
// now costs a commit the FULL mechanical suite's wall clock (up to
// DefaultPrecommitTimeout) instead of fail-first's own ~79-183s, roughly 3x
// on this box (TestPrecommit_ConcurrentPair_BothStagesRunEvenWhenFailFirstRejects
// covers it). That is deliberate and worth it: `violated`/`vacuous` is a rare
// TDD-discipline mistake (a new test that never went red), not the loop a
// TDD author actually repeats — the common loop is "mechanical fails because
// the impl is not written yet", which fail-first answers `red-proven` for
// (non-blocking) and which already let mechanical run every time, sequential
// or not. Paying 3x on the rare path buys back the ~79-183s fail-first alone
// used to cost EVERY commit, including every one of those common, already-
// fast red-proven ones.
//
// Called ONLY from gateRoot's non-cargo branch. A cargo root keeps its two
// suite stages sequential in gateRootCargo, deliberately NOT parallelized
// here: fail-first shares the workspace's own CARGO_TARGET_DIR with the
// mechanical stage (see precommit_failfirst.go's invalidateFailFirstArtifacts
// doc comment) and undoes the contamination with a `cargo clean` that runs
// AFTER the build's own target-lock hold has already been released — a
// window a concurrently-started mechanical run could slip into and read
// artifacts fail-first built from HEAD before they are removed. That is
// real shared state, not incidental sequencing, so cargo is out of scope for
// this change. Go (and every other ecosystem gateRoot's plain branch
// detects) has no such window: its fail-first worktree never shares a build
// cache with the working tree, and a plain `go test` (no -race) never
// touches the machine-wide build-slot pool runCargoLocked governs — so
// nothing on this path can contend for a build SLOT, and there is nothing to
// deadlock against there.
//
// There IS a real resource-contention risk this function's own launch
// creates that the slot pool cannot see: two CPU-heavy `go test` builds
// running at once can each take longer than either would running alone, and
// the gate's per-stage ceiling (DefaultPrecommitTimeout, 600s) does not grow
// to compensate — a stage that times out reports TIMEOUT, which means the
// commit was NOT tested, not merely slow. capGoTestParallelism below is the
// mitigation: it caps each invocation's own build (-p) and test (-parallel)
// parallelism, so the PAIR together asks the box for about what one
// uncapped `go test` already did, the same trade buildSlotCount's own doc
// comment describes for cargo's build-slot pool (two capped builds costing
// about what one uncapped one used to) — applied here because a Go root has
// no target-dir build lock to route that governor through in the first
// place. The cap's own VALUE is a reasoned starting point, not a measured
// one — see goTestJobsFor's doc comment and issue #546.
//
// When both stages reject, fail-first's verdict wins — the same priority
// the sequential version had, where fail-first ran and blocked before
// mechanical ever started.
func runFailFirstAndMechanicalConcurrently(gateName, repoRoot, root string, tests, srcs []string, runner Runner, run SuiteRunner) GateResult {
	run = capGoTestParallelism(run)
	var failFirstRes, mechRes GateResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		failFirstRes = failFirstStage(repoRoot, root, tests, srcs, run)
	}()
	go func() {
		defer wg.Done()
		mechRes = suiteStage(gateName, repoRoot, root, runner, run)
	}()
	wg.Wait()
	if failFirstRes.Blocked {
		return failFirstRes
	}
	return mechRes
}

// concurrentGoTestJobs is the parallelism ceiling capGoTestParallelism gives
// EACH of the two `go test` invocations this file launches at once — the
// thin caller that hands goTestJobsFor the box's real core count, kept
// separate so the formula itself can be pinned against literals rather than
// against runtime.NumCPU() (goTestJobsFor's own doc comment carries that
// reasoning, and the caveat this value is unmeasured).
func concurrentGoTestJobs() int {
	return goTestJobsFor(runtime.NumCPU())
}

// goTestJobsFor is the pure half-of-cpuCount formula, floored at 1: two
// processes each capped to half the box's cores then compete for about the
// same total the box already gave one uncapped `go test` — the identical
// headroom buildSlotCount's own doc comment describes for cargo's two-slot
// default ("N builds cost about what one uncapped build used to"), picked
// here for the same reason: this pair has no build lock to route a governor
// through, so the cap has to be a flag on the command itself.
//
// UNMEASURED, unlike buildSlotCount's cargo default (an empirically observed
// link-wave-OOM sweet spot): this number is a reasoned starting value, not a
// measured one, and there is a specific reason to doubt the cargo analogy
// transfers. The mechanical side of this pair is internal/tdd itself, which
// PR #544 made heavily parallel (318 t.Parallel() calls), and that suite is
// largely I/O- and subprocess-bound (git worktrees, spawned `go test`
// processes) rather than CPU-bound compute — halving -parallel for an
// I/O-wait-heavy suite can cost real wall clock without buying much of the
// CPU-contention safety this cap is for. That matters because
// failFirstWouldRun true (a staged test and its impl together) is ORDINARY
// TDD on this repo, not a rare case. Left open, tracked at issue #546 —
// measure on an idle box before treating half-of-NumCPU as settled.
func goTestJobsFor(cpuCount int) int {
	n := cpuCount / 2
	if n < 1 {
		return 1
	}
	return n
}

// isGoTestRunner reports whether r is a `go test` invocation — the only
// shape capGoTestParallelism's flags apply to (`go vet`, golangci-lint and
// every non-go runner take no -p/-parallel and must pass through untouched).
func isGoTestRunner(r Runner) bool {
	return r.Cmd == "go" && len(r.Args) > 0 && r.Args[0] == "test"
}

// capGoTestParallelism wraps run so every `go test` invocation it executes
// carries -p and -parallel capped to concurrentGoTestJobs(): -p bounds how
// many packages the BUILD compiles at once, -parallel bounds how many
// t.Parallel() tests the resulting binary runs at once — the two phases a
// `go test` invocation actually spends CPU on. Scoped to the caller's own
// run value only (runFailFirstAndMechanicalConcurrently's local variable),
// so neither fail-first's un-paired sequential path (gateRoot when failFirst
// is false) nor gateRootCargo's cargo builds are capped — neither of those
// contends with a concurrently-launched sibling the way this pair does.
func capGoTestParallelism(run SuiteRunner) SuiteRunner {
	n := concurrentGoTestJobs()
	return func(r Runner, dir string) SuiteResult {
		if isGoTestRunner(r) {
			r.Args = withGoJobCap(r.Args, n)
		}
		return run(r, dir)
	}
}

// withGoJobCap inserts -p=n and -parallel=n right after "test" — the same
// position withGoCIParity uses for its own flags — skipping either one
// already present, so applying this twice is harmless. Only the -flag=value
// form is checked: every flag-insertion site in this package (withGoCIParity
// included) produces that shape, and nothing anywhere produces the
// two-element ["-p", "N"] form, so there is no second shape to guard against.
func withGoJobCap(args []string, n int) []string {
	hasFlag := func(prefix string) bool {
		for _, a := range args[1:] {
			if strings.HasPrefix(a, prefix) {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0])
	if !hasFlag("-p=") {
		out = append(out, fmt.Sprintf("-p=%d", n))
	}
	if !hasFlag("-parallel=") {
		out = append(out, fmt.Sprintf("-parallel=%d", n))
	}
	out = append(out, args[1:]...)
	return out
}
