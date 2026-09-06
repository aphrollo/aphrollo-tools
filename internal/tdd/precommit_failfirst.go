package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The fail-first stage shares the repo's CARGO_TARGET_DIR with the mechanical
// stage, by design. This file is the one consequence that has to be undone:
// the artifacts fail-first builds from HEAD's source must not be left where
// the mechanical stage will read them as fresh.

// invalidateFailFirstArtifacts drops the packages the fail-first run just
// rebuilt from HEAD out of the SHARED target dir.
//
// The sharing is deliberate -- one target dir per repo, so a commit does not
// cold-build what is already built next door -- so the fix is not a second
// target dir but a narrow invalidation: exactly the packages this run named
// with -p, cleaned in the repo root where the artifacts actually live. Every
// other crate in the workspace keeps its warm cache.
//
// This costs nothing that correctness did not already require. Without
// fail-first the mechanical stage would compile the staged source itself; the
// clean only undoes an artifact built from DIFFERENT source that happened to
// land in its place.
func invalidateFailFirstArtifacts(run SuiteRunner, failFirst Runner, repoRoot string) {
	if failFirst.Cmd != "cargo" {
		return
	}
	pkgs := cargoPackagesInArgs(failFirst.Args)
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
	runCargoLocked(run, cleaner, ws, buildLockPrecommitDeadline, DefaultPrecommitTimeout)
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
// staged test changes, and runs the suite there. It returns (violated,
// conclusive): violated is true when the tests pass without the new source
// (they should fail first); conclusive is false when the check could not run,
// in which case the caller must not block. vacuous is #317's orthogonal
// third state: the run exited 0 having executed zero tests, which is
// neither a red proof nor a genuine violation and must be named as its own
// outcome rather than folded into "violated". This is failFirstViolatedAt
// with root == repoRoot, kept as its own name for the (still common)
// single-root case and for direct callers/tests.
func failFirstViolated(repoRoot string, tests []string, run SuiteRunner) (violated, conclusive, vacuous bool, vacuousPkgs []string, dur time.Duration) {
	return failFirstViolatedAt(repoRoot, repoRoot, tests, run)
}

// failFirstViolatedAt is failFirstViolated generalized to ONE project root
// within repoRoot's staged commit: a monorepo can stage a cargo crate's tests
// and a pytest tool's tests in the same commit, and each root must be judged
// by its OWN detected runner against its OWN staged test files — never by
// whichever toolchain happens to sit at the outer repo root. root == repoRoot
// reduces to the original single-root behavior exactly (execRootIn returns
// the worktree root unchanged), so failFirstViolated above is just this with
// that identity substitution. dur is the SuiteResult's own Duration when the
// suite actually ran (0 for every early return before that point) — found in
// review 2026-08-15: the fail-first stage line/gate.log entry always showed
// "0.0s" regardless of how long the worktree run actually took, because
// nothing threaded the real Duration out of here.
func failFirstViolatedAt(repoRoot, root string, tests []string, run SuiteRunner) (violated, conclusive, vacuous bool, vacuousPkgs []string, dur time.Duration) {
	wt := failFirstWorktreeDir(repoRoot)
	if wt == "" {
		var err error
		if wt, err = os.MkdirTemp("", "gate-failfirst-"); err != nil {
			return false, false, false, nil, 0
		}
	} else {
		// Stable per-repo path: a leftover registration from a crashed run
		// must go before `worktree add` will accept the path again.
		_, _ = git(repoRoot, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
	}
	defer os.RemoveAll(wt)
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		return false, false, false, nil, 0
	}
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", wt) }() // best-effort cleanup

	// The staged test diff applied onto HEAD: tests present, new source
	// absent — plus the staged DATA those tests read (see proofInputs), so a
	// test that would only go red against a stale template or fixture is not
	// mistaken for one that went red against missing code.
	diff, err := gitStaged(repoRoot, append(append([]string{}, tests...), proofInputs(repoRoot, tests)...))
	if err != nil || strings.TrimSpace(diff) == "" {
		return false, false, false, nil, 0
	}
	if err := gitApply(wt, diff); err != nil {
		return false, false, false, nil, 0 // can't reproduce the test state → don't block
	}

	execRoot, err := execRootIn(wt, repoRoot, root)
	if err != nil {
		return false, false, false, nil, 0
	}
	runner, ok := DetectRunner(execRoot)
	if !ok {
		return false, false, false, nil, 0
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
			return false, false, false, nil, 0
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
	res, waited, acquired := runCargoLocked(run, runner, execRoot, buildLockPrecommitDeadline, DefaultPrecommitTimeout)
	logLockWait("precommit", root, runner, waited)
	// Whatever the verdict, this run has just written artifacts built from
	// HEAD's source into the target dir the MECHANICAL stage is about to use,
	// with an mtime newer than the staged source. Drop them before returning:
	// left in place, cargo reads them as fresh and the mechanical stage runs
	// the new tests against the old implementation, which reports green for a
	// test that should be red.
	if !acquired {
		return false, false, false, nil, 0 // another cargo build holds the machine lock — no verdict either way
	}
	// Only now: a run that never acquired the lock built nothing, and
	// cleaning for it would evict a warm cache to undo writes that never
	// happened.
	defer invalidateFailFirstArtifacts(run, runner, repoRoot)
	if res.TimedOut {
		return false, false, false, nil, res.Duration // a killed run reaches no verdict either way
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
			return false, false, true, []string{fmt.Sprintf("(unreadable test-result stream: %v)", err)}, res.Duration
		}
		if len(names) > 0 {
			return false, false, true, names, res.Duration
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
	return res.Passed, true, false, nil, res.Duration
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
// at HEAD by construction. The mechanical stage still gates those.
func failFirstStage(repoRoot, root string, tests, srcs []string, run SuiteRunner) GateResult {
	if len(tests) > 0 && len(srcs) > 0 && stagedTestsAddDeclIn(repoRoot, tests) {
		ffCmd := ""
		if r, ok := DetectRunner(root); ok {
			ffCmd = cmdString(r)
		}
		violated, conclusive, vacuous, vacuousPkgs, dur := failFirstViolatedAt(repoRoot, root, tests, run)
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
		case vacuous:
			verdict = "vacuous-rejected"
		case conclusive && violated:
			verdict = "violated"
		case conclusive && !violated:
			verdict = "red-proven"
		}
		line := fmt.Sprintf("gate precommit: fail-first %s in %s → %s (%.1fs)", ffCmd, root, verdict, dur.Seconds())
		fmt.Fprintln(os.Stderr, line)
		appendGateLog("precommit", root, ffCmd, verdict, dur)
		if vacuous {
			return GateResult{Blocked: true, Message: vacuousFailFirstMessage(vacuousPkgs)}
		}
		if conclusive && violated {
			return GateResult{Blocked: true, Message: failFirstMessage}
		}
	}
	return GateResult{}
}
