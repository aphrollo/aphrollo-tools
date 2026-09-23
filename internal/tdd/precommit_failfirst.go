package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The fail-first stage shares the repo's CARGO_TARGET_DIR with the mechanical
// stage, by design. This file is the one consequence that has to be undone:
// the artifacts fail-first builds from HEAD's source must not be left where
// the mechanical stage will read them as fresh.

// invalidateFailFirstArtifacts drops the packages the fail-first run may have
// rebuilt from HEAD out of the SHARED target dir.
//
// The sharing is deliberate -- one target dir per repo, so a commit does not
// cold-build what is already built next door -- so the fix is not a second
// target dir but a narrow invalidation. "Narrow" used to mean exactly the
// packages this run named with -p, and "every other crate in the workspace
// keeps its warm cache" -- harryberg1n/borld#496 showed that rule wrong. The
// fail-first worktree applies ONLY the staged TEST diff onto HEAD
// (failFirstViolatedAt below), so a dependency crate whose non-test SOURCE
// the same commit also changed still builds there from HEAD's old source,
// even though narrowFailFirstTests picks -p purely from the staged tests and
// so never names it. That stale rlib lands in the shared target dir with a
// fresh mtime, and cargo's mtime fingerprinting then reads it as current for
// the mechanical stage right behind it: a green commit followed by a build
// that fails on symbols that exist in the tree.
//
// The corrected rule: invalidate the UNION of the -p'd packages and every
// package that owns a staged non-test source file ANYWHERE in this commit
// (failFirstInvalidationPackages), cleaned in the repo root where the
// artifacts actually live. A crate the commit did NOT touch at all still
// keeps its warm cache; every crate it touched, test or source, does not.
//
// "Anywhere in this commit" -- not just under this root -- is issue #722,
// the sibling of #496 above: a cargo workspace member carries its OWN
// Cargo.toml, so stagedRootGroupsErr's per-project-root grouping
// (FindProjectRoot stops at the FIRST directory holding a marker) splits one
// commit spanning two sibling members into TWO SEPARATE rootGroups, each
// processed by its own gateRoot call. The srcs a CALLER passes in is scoped
// to just its own root's rootGroup, so a commit that stages source in both
// alpha and its dependency beta never lets alpha's own invalidation see
// beta's staged file — even though alpha's fail-first worktree, building
// alpha, pulls beta in transitively and writes ITS stale rlib into the same
// shared target dir. workspaceStagedSources widens the search to every
// staged non-test source file in the WHOLE commit, not just this root's
// slice of it; cargoPackagesOwning already discards anything that root
// (walked via "..") does not actually own, so a file that belongs to some
// unrelated toolchain elsewhere in a monorepo costs nothing but a missed
// Cargo.toml stat.
//
// This costs nothing that correctness did not already require. Without
// fail-first the mechanical stage would compile the staged source itself; the
// clean only undoes an artifact built from DIFFERENT source that happened to
// land in its place.
func invalidateFailFirstArtifacts(run SuiteRunner, failFirst Runner, repoRoot, root string, srcs []string) {
	if failFirst.Cmd != "cargo" {
		return
	}
	pkgs := failFirstInvalidationPackages(failFirst.Args, root, repoRoot, workspaceStagedSources(repoRoot, srcs))
	if len(pkgs) == 0 {
		return
	}
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		return
	}
	args := []string{"clean"}
	for _, p := range pkgs {
		args = append(args, "-p", p)
	}
	cleaner := Runner{Cmd: "cargo", Args: args, Dir: ws}
	// No budget floor (the trailing zero): `cargo clean` runs no suite, so
	// there is no recorded suite duration that says anything about it.
	runCargoLocked(run, cleaner, ws, buildLockPrecommitDeadline, DefaultPrecommitTimeout, 0)
}

// workspaceStagedSources widens srcs (one project root's own staged non-test
// source, repoRoot-relative, as stagedRootGroupsErr scoped it) to the WHOLE
// staged index's non-test source, repoRoot-relative -- every sibling
// rootGroup's own files included. An unreadable index falls back to srcs
// alone, the same fail-open stance the rest of this stage takes: a query
// that cannot run must never look like "nothing else is staged".
// dedupeSorted collapses the overlap (every file already in srcs reappears
// in the whole-index read).
func workspaceStagedSources(repoRoot string, srcs []string) []string {
	staged, err := stagedFilesErr(repoRoot)
	if err != nil {
		return srcs
	}
	_, all := splitKinds(staged)
	return dedupeSorted(append(append([]string{}, srcs...), all...))
}

// failFirstInvalidationPackages is the actual invalidation set
// invalidateFailFirstArtifacts acts on: the union of every package the
// fail-first run named with -p (exactly what it rebuilt) and every package
// that OWNS a staged non-test source file (srcs, repoRoot-relative) --
// a dependency crate the fail-first worktree built from HEAD's stale source
// without ever naming it, per the design comment above. root anchors the
// ownership walk (cargoPackageFor resolves ".." components fine, so a file
// outside root still finds its OWNING package's own Cargo.toml as long as
// the relative path reaches it) — it need not be the file's own rootGroup.
// Deduped and sorted via dedupeSorted, so a package named both ways is
// cleaned once.
func failFirstInvalidationPackages(failFirstArgs []string, root, repoRoot string, srcs []string) []string {
	pkgs := cargoPackagesInArgs(failFirstArgs)
	owning := cargoPackagesOwning(root, toRootRelative(repoRoot, root, srcs))
	return dedupeSorted(append(pkgs, owning...))
}

// cargoPackagesInArgs is the -p values of a cargo argv, in order. The
// fail-first runner already resolved which packages it was building, so its
// own arguments name exactly what it wrote -- no second, possibly disagreeing
// derivation from the staged paths.
func cargoPackagesInArgs(args []string) []string {
	var pkgs []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-p" || args[i] == "--package" {
			pkgs = append(pkgs, args[i+1])
			i++
		}
	}
	return pkgs
}

// failFirstViolated builds a throwaway worktree at HEAD, applies ONLY the
// staged test changes, and runs the suite there. Its failFirstOutcome carries
// violated (the tests pass without the new source, so they never went RED),
// conclusive (false when the check could not run, in which case the caller
// must not block), #317's orthogonal vacuous third state (the run exited 0
// having executed zero tests, neither a red proof nor a genuine violation),
// the suite's measured duration, and the argv it ran. This is failFirstViolatedAt
// with root == repoRoot, kept as its own name for the (still common)
// single-root case and for direct callers/tests. srcs is the commit's staged
// non-test source under root (repoRoot-relative) — passed through only so the
// post-run invalidation can see the packages it owns; it never enters the
// worktree.
func failFirstViolated(repoRoot string, tests, srcs []string, run SuiteRunner) failFirstOutcome {
	return failFirstViolatedAt(repoRoot, repoRoot, tests, srcs, run)
}

// failFirstViolatedAt is failFirstViolated generalized to ONE project root
// within repoRoot's staged commit: a monorepo can stage a cargo crate's tests
// and a pytest tool's tests in the same commit, and each root must be judged
// by its OWN detected runner against its OWN staged test files — never by
// whichever toolchain happens to sit at the outer repo root. root == repoRoot
// reduces to the original single-root behavior exactly (execRootIn returns
// the worktree root unchanged), so failFirstViolated above is just this with
// that identity substitution. outcome.dur is the SuiteResult's own Duration
// when the suite actually ran (0 for every early return before that point) —
// found in review 2026-08-15: the fail-first stage line/gate.log entry always
// showed "0.0s" regardless of how long the worktree run actually took, because
// nothing threaded the real Duration out of here. outcome.cmd is the same
// thread for the argv (#567): the narrowed command this function built and
// ran, so the stage line names what was proven rather than the profile's
// unnarrowed `go test ./...`.
func failFirstViolatedAt(repoRoot, root string, tests, srcs []string, run SuiteRunner) failFirstOutcome {
	wt := failFirstWorktreeDir(repoRoot)
	if wt == "" {
		var err error
		if wt, err = os.MkdirTemp("", "gate-failfirst-"); err != nil {
			return failFirstOutcome{}
		}
	} else {
		// Stable per-repo path: a leftover registration from a crashed run
		// must go before `worktree add` will accept the path again.
		_, _ = git(repoRoot, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
	}
	defer os.RemoveAll(wt)
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		return failFirstOutcome{}
	}
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", wt) }() // best-effort cleanup

	// The staged test diff applied onto HEAD: tests present, new source
	// absent — plus the staged DATA those tests read (see proofInputs), so a
	// test that would only go red against a stale template or fixture is not
	// mistaken for one that went red against missing code.
	diff, err := gitStaged(repoRoot, append(append([]string{}, tests...), proofInputs(repoRoot, tests)...))
	if err != nil || strings.TrimSpace(diff) == "" {
		return failFirstOutcome{}
	}
	if err := gitApply(wt, diff); err != nil {
		return failFirstOutcome{} // can't reproduce the test state → don't block
	}

	execRoot, err := execRootIn(wt, repoRoot, root)
	if err != nil {
		return failFirstOutcome{}
	}
	runner, ok := DetectRunner(execRoot)
	if !ok {
		return failFirstOutcome{}
	}
	relTests := toRootRelative(repoRoot, root, tests)
	if runner.Cmd == "cargo" {
		// A file no [package] owns is excluded, never a trigger to widen the
		// fail-first run to the whole workspace — narrowFailFirstTests already
		// excludes unowned files internally, but when NOTHING staged is owned
		// it comes back unnarrowed (see its doc comment), and running THAT
		// would be exactly the unrunnable-in-time full suite this stage
		// exists to avoid. Check ownership before ever invoking it.
		if len(cargoPackagesOwning(execRoot, relTests)) == 0 {
			return failFirstOutcome{}
		}
	}
	// Scope to the staged TEST targets under judgment: the full unnarrowed
	// suite (esp. cargo nextest over a large workspace) is 10-20 minutes,
	// blows this stage's own timeout, and fails open having proven nothing.
	runner = narrowFailFirstTests(runner, execRoot, relTests)
	// The fail-first run is a GATE run: it compiles and runs the same tests
	// under the same contention, so it takes the same profile.
	if runner.Cmd == "cargo" {
		profileWs := runner.Dir
		if profileWs == "" {
			profileWs = execRoot
		}
		runner = withGateProfile(runner, profileWs)
	}
	// The worktree lives OUTSIDE the repo, so cargo's default would put a
	// brand-new target/ inside it and cold-build the world on every commit.
	// Name the repo's own resolved target explicitly.
	if runner.Cmd == "cargo" {
		if dir := resolvedDevTarget(repoRoot); dir != "" {
			prev, had := os.LookupEnv("CARGO_TARGET_DIR")
			os.Setenv("CARGO_TARGET_DIR", dir)
			defer func() {
				if had {
					os.Setenv("CARGO_TARGET_DIR", prev)
				} else {
					os.Unsetenv("CARGO_TARGET_DIR")
				}
			}()
		}
	}
	// The switches the repo declares its gated suites need (#656). Without
	// them a suite behind an env switch self-skips in this worktree and the
	// proof measures nothing — see failfirst_env.go. Exported around the run
	// only, the same way CARGO_TARGET_DIR is just above.
	defer exportFailFirstEnv(repoRoot)()
	// No budget floor (the trailing zero): this run is the staged test
	// proved RED against HEAD's source, and it records its verdict under
	// this stage's own name rather than as a suite duration for a command
	// the floor could be derived from. It keeps today's arithmetic.
	res, waited, acquired := runCargoLocked(run, runner, execRoot, buildLockPrecommitDeadline, DefaultPrecommitTimeout, 0)
	logLockWait("precommit", root, runner, waited)
	// Whatever the verdict, this run has just written artifacts built from
	// HEAD's source into the target dir the MECHANICAL stage is about to use,
	// with an mtime newer than the staged source. Drop them before returning:
	// left in place, cargo reads them as fresh and the mechanical stage runs
	// the new tests against the old implementation, which reports green for a
	// test that should be red.
	if !acquired {
		// Another build held the machine lock for the whole wait, so the
		// proof never ran. Named as its own stand-down: the caller refuses
		// the commit with the remedy for a queued box, not the one for a
		// slow suite (#561).
		return failFirstOutcome{cmd: cmdString(runner), standDown: failFirstNoBuildSlot, waited: waited}
	}
	// Only now: a run that never acquired the lock built nothing, and
	// cleaning for it would evict a warm cache to undo writes that never
	// happened.
	defer invalidateFailFirstArtifacts(run, runner, repoRoot, root, srcs)
	if res.TimedOut {
		// A killed run reaches no verdict either way — and measured
		// nothing, so the caller refuses rather than landing the commit on
		// an unproven test (#561).
		return failFirstOutcome{dur: res.Duration, cmd: cmdString(runner), standDown: failFirstOverBudget}
	}
	// #317: the proof worktree exited 0 having executed zero tests — a
	// narrowed -run/-k/name filter matching nothing, say. That is not a red
	// proof (nothing ran to go red) and not a genuine violation either (the
	// test never actually passed against the pre-edit code, because it never
	// ran at all): name it as its own outcome, per package/target
	// (vacuousNames, vacuous_dispatch.go — Go, cargo, pytest, vitest alike).
	if res.Passed {
		names, err := vacuousNames(runner, res)
		if err != nil {
			// Same stance as runSuiteStage: a stream this function could not
			// finish reading is not distinguishable from one that measured
			// nothing, so it is reported through the SAME vacuous/blocking
			// path rather than falling open into "no verdict either way" —
			// #317's point is exactly that an unmeasured run must never look
			// like a pass, and "unreadable" is the same category as
			// "measured nothing".
			return failFirstOutcome{vacuous: true, vacuousPkgs: []string{fmt.Sprintf("(unreadable test-result stream: %v)", err)}, dur: res.Duration, cmd: cmdString(runner)}
		}
		if len(names) > 0 {
			return failFirstOutcome{vacuous: true, vacuousPkgs: names, dur: res.Duration, cmd: cmdString(runner)}
		}
		// #656: the tests WERE selected, they ran, and every one of them
		// skipped itself — an env-gated device suite in a worktree that does
		// not carry the switch. Exit 0 with nothing asserted is not a pass
		// against HEAD, so it must never reach the violation below.
		skipped, err := skippedOnlyNames(runner, res)
		if err != nil {
			return failFirstOutcome{vacuous: true, vacuousPkgs: []string{fmt.Sprintf("(unreadable test-result stream: %v)", err)}, dur: res.Duration, cmd: cmdString(runner), runner: runner}
		}
		if len(skipped) > 0 {
			return failFirstOutcome{skipped: true, skippedPkgs: skipped, dur: res.Duration, cmd: cmdString(runner), runner: runner}
		}
	}
	// Tests PASS without the new source ⇒ they never went RED ⇒ violation.
	//
	// SuiteResult carries only Passed/Output, with no couldn't-run signal, so a
	// suite that failed to RUN (e.g. a vitest/jest worktree with no node_modules)
	// is indistinguishable from one that ran and failed: both surface as
	// Passed=false ⇒ (violated=false, conclusive=true). That mislabels a
	// non-running suite as a conclusive non-violation rather than inconclusive,
	// but it fails in the safe direction — non-violation never blocks — so the
	// gate stays fail-open. Correcting the label needs a distinct couldn't-run
	// signal on SuiteResult, which is left for a runner-contract change.
	return failFirstOutcome{violated: res.Passed, conclusive: true, dur: res.Duration, cmd: cmdString(runner), runner: runner}
}

// execRootIn maps root (a project root under repoRoot) to its equivalent
// directory inside the fail-first worktree wt, which mirrors repoRoot's tree
// at HEAD. root == repoRoot maps to wt itself.
func execRootIn(wt, repoRoot, root string) (string, error) {
	rel, err := filepath.Rel(repoRoot, root)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return wt, nil
	}
	return filepath.Join(wt, rel), nil
}

// failFirstWorktreeDir returns the stable per-repo path for the fail-first
// worktree, under the state dir. Stability is the point: cargo fingerprints
// bake in absolute source paths, so a fresh MkdirTemp per commit cold-rebuilds
// the workspace crates every time even with a warm CARGO_TARGET_DIR. "" when
// there is no state dir (the caller then falls back to a temp dir).
func failFirstWorktreeDir(repoRoot string) string {
	base := stateDir()
	if base == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(repoRoot))
	dir := filepath.Join(base, "failfirst-wt", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return ""
	}
	writeGateOrigin(dir, repoRoot)
	return dir
}

// failFirstStage runs the fail-first check for ONE project root's staged
// files, in a throwaway worktree at HEAD. That worktree has no node_modules
// — worktrees don't share gitignored deps and we do NOT `pnpm install` per
// commit (too slow) — so for vitest/jest repos the suite can't run there and
// fail-first is effectively Go-only. It still fails OPEN (an unrunnable
// suite is inconclusive, never a block).
//
// Zig fails OPEN here by construction, and deliberately so. Its tests are
// `test "..." {}` blocks INLINE in src/*.zig, so an inline-test commit stages
// only Source-classified .zig files (ClassifyFile flips only *_test.zig /
// tests/*.zig to Test) — len(tests) is 0 and this guard skips fail-first.
// That is correct: the test and the impl it exercises live in the SAME hunk
// and cannot be cleanly separated, so applying "just the test" to a HEAD
// worktree would drag the impl along and make the check meaningless. An
// EXPLICIT tests/*.zig staged alongside its source DOES enter fail-first, but
// an integration test cannot compile without the source it imports, so the
// worktree run fails to RUN and falls through the unrunnable-suite path above
// (Passed=false ⇒ violated=false). Either way fail-first never false-blocks a
// Zig commit; the mechanical stage's full `zig build test` is the real gate.
// Fail-first judges NEW tests only, so it fires when the staged test
// changes actually ADD a test declaration. A declaration-free test edit
// (lint reflow, gofmt, a renamed local) has nothing to prove RED — and
// judging it would false-block, since a reformatted EXISTING test passes
// at HEAD by construction. The merge gate's suite still covers those.
// failFirstWouldRun reports whether failFirstStage will actually launch a
// worktree run for this staged change, rather than no-op through it — the
// same guard failFirstStage itself opens on, named as its own predicate so a
// caller can ask the question without launching anything. A commit with no
// staged test, or one whose test adds no new declaration, never runs
// fail-first at all.
func failFirstWouldRun(repoRoot string, tests, srcs []string) bool {
	return len(tests) > 0 && len(srcs) > 0 && stagedTestsAddDeclIn(repoRoot, tests)
}

func failFirstStage(repoRoot, root string, tests, srcs []string, run SuiteRunner) GateResult {
	if failFirstWouldRun(repoRoot, tests, srcs) {
		out := failFirstViolatedAt(repoRoot, root, tests, srcs, run)
		// The argv the proof ACTUALLY ran, which is narrowed to the staged
		// tests and is not the profile's own command. A run that never
		// started names nothing, so the line falls back to the detected
		// profile rather than printing an empty command.
		ffCmd := out.cmd
		if ffCmd == "" {
			if r, ok := DetectRunner(root); ok {
				ffCmd = cmdString(r)
			}
		}
		// A proof that measured nothing is refused, not folded into a
		// fail-open pass — each cause with its own remedy (#561). Both
		// print and log through verdictFor, so neither reaches the
		// inconclusive line below.
		switch out.standDown {
		case failFirstNoBuildSlot:
			return failFirstNoBuildSlotRefusal(root, ffCmd, out)
		case failFirstOverBudget:
			return failFirstOverBudgetRefusal(root, ffCmd, out)
		}
		// The gate must never be silent about a stage it ran, whatever the
		// verdict — a session watching stderr needs to see fail-first
		// happened, not infer it from the commit's exit code. Timeout and
		// "nothing was runnable" both collapse to "inconclusive (fail-open)"
		// here: failFirstViolatedAt's (violated, conclusive) pair doesn't
		// carry WHY it was inconclusive, and neither ever blocks, so the
		// coarser label loses no decision-relevant information. vacuous is
		// checked first: #317's "executed zero tests" is neither a red proof
		// nor a genuine violation, and reporting it as either would misname
		// the actual defect.
		verdict := "inconclusive (fail-open)" // standdown-logged: default value; every path below still reaches the appendGateLog(verdict) call after the switch
		switch {
		case out.vacuous:
			verdict = "vacuous-rejected"
		case out.skipped:
			// #656: its own token, never red-proven and never violated —
			// `gate stats` must be able to count the proofs that ran and
			// measured nothing separately from the ones that proved a red.
			verdict = AllTestsSkipped
		case out.conclusive && out.violated:
			verdict = "violated"
		case out.conclusive && !out.violated:
			verdict = "red-proven"
		}
		line := fmt.Sprintf("[fail-first] gate precommit: %s in %s → %s (%.1fs)", ffCmd, root, verdict, out.dur.Seconds())
		fmt.Fprintln(os.Stderr, line)
		appendGateLog("precommit", root, ffCmd, verdict, out.dur)
		if out.vacuous {
			return GateResult{Blocked: true, Message: vacuousFailFirstMessage(out.vacuousPkgs)}
		}
		if out.skipped {
			return GateResult{Blocked: true, Message: allTestsSkippedMessage(out.skippedPkgs, out.runner)}
		}
		if out.conclusive && out.violated {
			return GateResult{Blocked: true, Message: failFirstViolationMessage(out.runner)}
		}
	}
	return GateResult{}
}
