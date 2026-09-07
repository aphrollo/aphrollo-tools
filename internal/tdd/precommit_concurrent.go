package tdd

import "sync"

// runFailFirstAndMechanicalConcurrently runs gateRoot's two suite stages —
// the fail-first RED proof (a throwaway worktree at HEAD, staged tests only
// applied) and the mechanical suite (the working tree, staged source AND
// tests) — IN PARALLEL rather than one after the other (issue #535). The two
// read different trees and neither writes state the other reads, so their
// sequencing was incidental: on this box fail-first alone cost 79-183s of
// every commit's wall clock, paid even on commits mechanical was always
// going to reject anyway.
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
// nothing on this path can contend for a build slot, and there is nothing to
// deadlock against.
//
// Both stages run to completion even when one has already rejected: a
// short-circuit here would spend fail-first's own wall clock on every
// failing commit exactly like the sequential version did, throwing away the
// saving on the one path (a red commit) an author repeats most. When BOTH
// reject, fail-first's verdict wins — the same priority the sequential
// version had, where fail-first ran and blocked before mechanical ever
// started.
func runFailFirstAndMechanicalConcurrently(gateName, repoRoot, root string, tests, srcs []string, runner Runner, run SuiteRunner) GateResult {
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
