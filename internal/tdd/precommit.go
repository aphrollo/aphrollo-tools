package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GateResult is the verdict of a git-time gate (precommit, prepush). A blocked
// action always carries a Message explaining what failed and how to proceed.
// Message can also be set with Blocked false — a mechanical-stage TIMEOUT
// fails open (the commit is never blocked over a stopwatch) but must never be
// silent about it either, so the fail-open line rides in Message even though
// nothing was actually rejected.
type GateResult struct {
	Blocked bool
	Message string
}

const failFirstMessage = "TDD fail-first: this commit adds tests AND implementation, but the new tests " +
	"PASS against the pre-edit code (HEAD) — so they are not actually pinning the new behavior. " +
	"A test that never went RED can't prove the implementation. Write the test first and watch it fail, " +
	"or split the test into its own earlier commit."

// rootGroup is one project root's staged Test/Source files (repo-root-
// relative paths), the unit both Precommit and Mechanical iterate.
type rootGroup struct {
	root        string
	tests, srcs []string
}

// stagedRootGroups groups repoRoot's staged Source/Test files by
// FindProjectRoot — the single place both Precommit (fail-first +
// mechanical) and Mechanical (the merge gate: mechanical only) derive their
// per-root work from, so the grouping rule can never drift between them. A
// monorepo can stage files under several DIFFERENT project roots in one
// commit (a cargo workspace with a pytest tool living inside it); judging the
// whole commit by DetectRunner(repoRoot) alone means whichever marker sits at
// the outer repo root decides EVERY staged file's toolchain — a python-only
// commit under a Bevy-sized cargo workspace then pays a full `cargo nextest
// run` (20 minutes) for a change cargo has nothing to do with.
func stagedRootGroups(repoRoot string) []rootGroup {
	staged := stagedFiles(repoRoot)
	if len(staged) == 0 {
		return nil
	}
	tests, srcs := splitKinds(staged)
	all := append(append([]string{}, tests...), srcs...)
	var groups []rootGroup
	for _, root := range stagedProjectRoots(repoRoot, all) {
		groups = append(groups, rootGroup{
			root:  root,
			tests: filesUnderRoot(repoRoot, root, tests),
			srcs:  filesUnderRoot(repoRoot, root, srcs),
		})
	}
	return groups
}

// Precommit runs the commit-time TDD wall in repoRoot:
//
//  1. Fail-first — ONLY when the staged change adds both tests and source.
//     The new tests are applied to a throwaway worktree at HEAD (without the
//     source change) and run; if they PASS there, they don't require the new
//     code, which is a fail-first violation. Triggering only on a combined
//     test+source commit is also the amendment fix: an impl-only amend stages
//     no test, so fail-first never re-judges already-committed tests.
//  2. Mechanical — the related tests for the staged source+test files must pass.
//     A commit that stages no source AND no test (docs/yaml only) skips this
//     stage entirely.
//
// Any inability to VERIFY fail-first (worktree/apply error) fails OPEN: the
// gate never blocks because its own tooling tripped. run is injected so the
// mechanical and worktree runs are testable.
func Precommit(repoRoot string, run SuiteRunner) GateResult {
	// Concluding a CONFLICTED merge/cherry-pick/revert with `git commit`
	// fires git's pre-commit hook (pre-merge-commit only fires for an
	// AUTOMATIC, conflict-free merge commit) — task A10. Judging the WHOLE
	// lane diff against HEAD with fail-first is meaningless here (the
	// individual commits being merged already went through their own
	// fail-first when authored) and can cost many minutes of throwaway
	// worktree builds for nothing. Run EXACTLY the pre-merge routine
	// instead: Mechanical only, no fail-first, no anti-cheat.
	if ref := mergeInProgressRef(repoRoot); ref != "" {
		fmt.Fprintf(os.Stderr, "gate precommit: merge in progress (%s) — running the pre-merge routine (mechanical only)\n", ref)
		return Mechanical(repoRoot, run)
	}

	var notes []string
	collect := func(res GateResult) (blocked bool) {
		if res.Message != "" {
			notes = append(notes, res.Message)
		}
		return res.Blocked
	}
	// Cheapest first, and BEFORE the has-code check: a commit staging only a
	// baseline file or a doc is exactly the shape that raises a ceiling by
	// hand or lands a law regression, and it used to return here having
	// answered to nothing.
	if res := baselineStage("precommit", repoRoot); collect(res) {
		return res
	}
	if res := ratchetStage("precommit", repoRoot); collect(res) {
		return res
	}
	if res := docsCheckStage("precommit", repoRoot); collect(res) {
		return res
	}

	groups := stagedRootGroups(repoRoot)
	if len(groups) == 0 {
		return GateResult{Message: strings.Join(notes, "\n")}
	}

	// Anti-cheat: block a newly-INTRODUCED suppression before spending the suite
	// on it. Scoped to added lines, so a suppression that already lived in the
	// tree never blocks an unrelated later commit — only one this change adds.
	if msg := newSuppression(repoRoot); msg != "" {
		return GateResult{Blocked: true, Message: msg}
	}

	for _, g := range groups {
		if res := gateRoot("precommit", repoRoot, g, run, true); collect(res) {
			return res
		}
	}
	return GateResult{Message: strings.Join(notes, "\n")}
}

// Mechanical runs ONLY the mechanical stage of the commit-time TDD wall,
// grouped by project root exactly like Precommit — but with NO fail-first (a
// fresh test's RED/GREEN belongs to the AUTHORING commit, already proven
// there by Precommit) and NO anti-cheat suppression scan (same reasoning:
// both are judgments about how a change was AUTHORED, not whether the
// resulting combined tree still compiles and passes, which is the only thing
// a merge can meaningfully re-check). Used by the pre-merge-commit gate: two
// branches that each individually passed Precommit can still integrate
// broken — that's what a merge combining them can introduce, and only the
// mechanical stage catches it. A merge whose staged set has nothing to test
// (e.g. a docs-only merge) says so explicitly rather than returning a bare
// empty result indistinguishable from "the gate never ran".
func Mechanical(repoRoot string, run SuiteRunner) GateResult {
	// The cheapest possible rejection comes first: a lane with no mutation
	// proof is refused before a single suite compiles.
	if res := mutationReceiptStage(repoRoot); res != nil {
		// Printing it here too would state the same paragraph twice: the hook
		// that called this prints what it is given.
		appendGateLog("premergecommit", repoRoot, "mutation-receipt", "receipt-rejected", 0)
		return *res
	}
	var notes []string
	// Same order as Precommit, and for the same reason: a merge carrying only
	// a raised baseline or a law regression must answer for it before the
	// has-code check can wave it through.
	if res := baselineStage("premergecommit", repoRoot); res.Blocked {
		return res
	}
	if res := ratchetStage("premergecommit", repoRoot); res.Blocked {
		return res
	} else if res.Message != "" {
		notes = append(notes, res.Message)
	}
	if res := docsCheckStage("premergecommit", repoRoot); res.Blocked {
		return res
	} else if res.Message != "" {
		notes = append(notes, res.Message)
	}

	groups := stagedRootGroups(repoRoot)
	if len(groups) == 0 {
		const line = "gate premergecommit: nothing to test (no staged source or test files)"
		fmt.Fprintln(os.Stderr, line)
		notes = append(notes, line)
		return GateResult{Message: strings.Join(notes, "\n")}
	}
	for _, g := range groups {
		res := gateRoot("premergecommit", repoRoot, g, run, false)
		if res.Blocked {
			return res
		}
		if res.Message != "" {
			notes = append(notes, res.Message)
		}
	}
	return GateResult{Message: strings.Join(notes, "\n")}
}

// mutationReceiptStage judges the lane's mutation receipt, for workspaces
// that asked for it (`mutation-receipt = true`). nil means "allow": the
// workspace has not opted in, or the receipt covers this tree.
func mutationReceiptStage(repoRoot string) *GateResult {
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	if !cargoAphrolloFlag(ws, "mutation-receipt") {
		return nil
	}
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		// The gate failed on its own inputs, so it says which input: a clean
		// automerge has no MERGE_HEAD yet, and only GIT_REFLOG_ACTION names
		// the branch coming in.
		return blockReceipt("no lane tip to look a receipt up by (neither .git/MERGE_HEAD nor %s names a merged branch)", reflogActionEnv)
	}
	// The repo's shared git COMMON dir, never repoRoot's own directory name:
	// a linked worktree is routinely named unlike the repo (a lane checked
	// out at `.worktrees/borld/eol`), but every worktree of one repo shares
	// this one directory, which is what actually identifies "one repo" to
	// sameRepo.
	return checkMutationReceipt(commonGitDir(repoRoot), tip.Tree, mergeBaseSHA(repoRoot, tip.Rev))
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
		violated, conclusive, dur := failFirstViolatedAt(repoRoot, root, tests, run)
		// The gate must never be silent about a stage it ran, whatever the
		// verdict — a session watching stderr needs to see fail-first
		// happened, not infer it from the commit's exit code. Timeout and
		// "nothing was runnable" both collapse to "inconclusive (fail-open)"
		// here: failFirstViolatedAt's (violated, conclusive) pair doesn't
		// carry WHY it was inconclusive, and neither ever blocks, so the
		// coarser label loses no decision-relevant information.
		verdict := "inconclusive (fail-open)"
		switch {
		case conclusive && violated:
			verdict = "violated"
		case conclusive && !violated:
			verdict = "red-proven"
		}
		line := fmt.Sprintf("gate precommit: fail-first %s in %s → %s (%.1fs)", ffCmd, root, verdict, dur.Seconds())
		fmt.Fprintln(os.Stderr, line)
		appendGateLog("precommit", root, ffCmd, verdict, dur)
		if conclusive && violated {
			return GateResult{Blocked: true, Message: failFirstMessage}
		}
	}
	return GateResult{}
}

// gateRoot runs ONE project root's gate stages in COST order, stopping at
// the first rejection:
//
//  1. cargo fmt --check   (milliseconds)
//  2. the always-run guard packages, as their OWN invocation (a pure crate;
//     bundling it into `-p ratchet -p client` made it wait for client to link)
//  3. cargo clippy on the crates declared clippy-clean
//  4. fail-first RED proof (precommit only, and only when staged tests add a
//     declaration)
//  5. the touched crates' suites — full test build, link and run, the
//     heaviest thing the gate does
//
// Before this the heaviest stage ran first, so a commit with a formatting
// slip paid the whole test build to be told about a space. gateName
// ("precommit"/"premergecommit") names the calling gate in every stderr line
// and gate.log entry; failFirst is false for the merge gate, whose commits
// were each already judged when authored.
func gateRoot(gateName, repoRoot string, g rootGroup, run SuiteRunner, failFirst bool) GateResult {
	// Changes-gate: a docs/yaml-only commit (no staged source AND no staged
	// test) under this root has nothing to check.
	if len(g.tests) == 0 && len(g.srcs) == 0 {
		return GateResult{}
	}
	runner, ok := DetectRunner(g.root)
	if !ok {
		fmt.Fprintf(os.Stderr, "gate %s: %s → skipped (no detected runner)\n", gateName, g.root)
		return GateResult{}
	}
	rootFiles := append(append([]string{}, g.tests...), g.srcs...)

	if runner.Cmd == "cargo" {
		plan, ok := planCargoStages(gateName, repoRoot, g.root, rootFiles)
		if !ok {
			return GateResult{}
		}
		if res := cargoQualityStage(gateName, plan.ws, g.root, plan.touched, run, repoRoot, qualityFmt); res.Blocked {
			return res
		}
		if res := alwaysRunStage(gateName, repoRoot, g.root, plan, run); res.Blocked {
			return res
		}
		if res := cargoQualityStage(gateName, plan.ws, g.root, plan.touched, run, repoRoot, qualityClippy); res.Blocked {
			return res
		}
		if res := workspaceCheckStage(gateName, repoRoot, g.root, plan, run); res.Blocked {
			return res
		}
		if failFirst {
			if res := failFirstStage(repoRoot, g.root, g.tests, g.srcs, run); res.Blocked {
				return res
			}
		}
		if res := suiteStage(gateName, repoRoot, g.root, plan.suiteRunner(), run); res.Blocked {
			return res
		}
		return doctestStage(gateName, repoRoot, g.root, plan, run)
	}

	// Scope the mechanical run to the related tests of the staged
	// source+test files: commit-time is a fast scoped check; CI runs the
	// full suite at submit as the authoritative gate. A runner with no
	// related mode (or an unknown command) falls back to the full suite
	// unchanged.
	if scoped, narrowed := narrowToStaged(runner, g.root, toRootRelative(repoRoot, g.root, rootFiles)); narrowed {
		runner = scoped
	}
	// CI parity for a Go root: the same vet and lint the branch is judged by,
	// both cheaper than the suite and therefore ahead of it.
	if runner.Cmd == "go" {
		if res := goQualityStage(gateName, g.root, run); res.Blocked {
			return res
		}
	}
	if failFirst {
		if res := failFirstStage(repoRoot, g.root, g.tests, g.srcs, run); res.Blocked {
			return res
		}
	}
	return suiteStage(gateName, repoRoot, g.root, runner, run)
}

// cargoStagePlan is what the cargo stages of one root need: the workspace
// commands run from, the packages this commit TOUCHED (never the always-run
// additions — no staged file belongs to those), and the guard packages the
// workspace declares.
type cargoStagePlan struct {
	ws        string
	touched   []string
	alwaysRun []string
}

// suiteRunner is the touched crates' own scoped test command — the guard
// packages deliberately absent, since they run as their own cheap stage.
func (p cargoStagePlan) suiteRunner() Runner {
	args := cargoVerbArgs(p.ws)
	for _, pkg := range p.touched {
		args = append(args, "-p", pkg)
	}
	return withGateProfile(Runner{Cmd: "cargo", Args: args, Dir: p.ws}, p.ws)
}

// guardRunner is the always-run packages' own invocation.
func (p cargoStagePlan) guardRunner() Runner {
	args := cargoVerbArgs(p.ws)
	for _, pkg := range p.alwaysRun {
		args = append(args, "-p", pkg)
	}
	return withGateProfile(Runner{Cmd: "cargo", Args: args, Dir: p.ws}, p.ws)
}

// planCargoStages resolves package ownership for a cargo root. Ownership is
// judged PER FILE against the [package] Cargo.toml that covers it: a file no
// package owns (a virtual workspace manifest, a path outside any member) is
// SKIPPED with a stderr note, never a trigger to widen the run to the whole
// workspace. ok=false means nothing staged here is owned by any package.
func planCargoStages(gateName, repoRoot, root string, rootFiles []string) (cargoStagePlan, bool) {
	owned, unowned := cargoOwnedFiles(repoRoot, root, rootFiles)
	for _, f := range unowned {
		fmt.Fprintf(os.Stderr, "gate %s: %s has no owning cargo package — not tested\n", gateName, f)
	}
	if len(owned) == 0 {
		return cargoStagePlan{}, false
	}
	// The actual WORKSPACE root: a checked-in .config/nextest.toml and the
	// workspace's Cargo.lock live there, not in a member crate's own
	// directory. State/mech-cache keys still use the crate root.
	ws := cargoWorkspaceRoot(root)
	return cargoStagePlan{
		ws:        ws,
		touched:   cargoPackagesOwning(root, toRootRelative(repoRoot, root, owned)),
		alwaysRun: cargoAlwaysRunPackages(ws),
	}, true
}

// alwaysRunStage runs the workspace's declared guard packages as their own
// invocation, before the touched crates' heavier one. A guard package owns
// no staged file, so ownership scoping alone would run it only when someone
// edits the guard itself — precisely when its invariant is not at risk.
func alwaysRunStage(gateName, repoRoot, root string, plan cargoStagePlan, run SuiteRunner) GateResult {
	if len(plan.alwaysRun) == 0 {
		return GateResult{}
	}
	return runSuiteStage(gateName, "always-run", repoRoot, root, plan.guardRunner(), run)
}

// workspaceCheckStage compiles the WHOLE workspace's code and tests, without
// codegen. The suites only cover the crates a commit touched, so a change
// that breaks a crate nobody staged lands green: borld's
// forge_jbeam/tests/conformance.rs reached main not compiling because a lane
// added a struct field and the gate ran only that lane's crates. A check is
// the cheapest stage that can see the whole graph — tens of seconds warm,
// against minutes for the suites — so it sits between clippy and fail-first.
func workspaceCheckStage(gateName, repoRoot, root string, plan cargoStagePlan, run SuiteRunner) GateResult {
	ws := plan.ws
	if ws == "" {
		ws = root
	}
	// clippy SUBSUMES check — a compile error fails it just the same — and it
	// is the only way to enforce the two lints that carry project laws
	// (clippy.toml's disallowed methods and types) across crates that are not
	// on the clippy-clean list. Nothing else is denied here: -D warnings
	// belongs to the per-crate stage, where a crate has actually reached zero.
	runner := Runner{Cmd: "cargo", Args: []string{
		"clippy", "--workspace", "--tests", "--",
		"-D", "clippy::disallowed_methods", "-D", "clippy::disallowed_types",
	}, Dir: ws}
	return runSuiteStage(gateName, "check", repoRoot, root, runner, run)
}

// doctestStage runs the touched crates' doctests, which the nextest-based
// suite stage above cannot: nextest does not run them at all, so a
// `compile_fail` proof would otherwise never execute.
func doctestStage(gateName, repoRoot, root string, plan cargoStagePlan, run SuiteRunner) GateResult {
	ws := plan.ws
	if ws == "" {
		ws = root
	}
	for _, runner := range doctestRunners(ws, plan.touched) {
		if res := runSuiteStage(gateName, "doctest", repoRoot, root, runner, run); res.Blocked {
			return res
		}
	}
	return GateResult{}
}

// suiteStage runs the touched crates' own suites — the heaviest stage, and
// therefore the last.
func suiteStage(gateName, repoRoot, root string, runner Runner, run SuiteRunner) GateResult {
	return runSuiteStage(gateName, "mechanical", repoRoot, root, runner, run)
}

// runSuiteStage is the shared body every suite-running stage uses: green
// cache, build slot, and one honest verdict line. stage names it in stderr
// and gate.log ("mechanical", "always-run"), so a block says which stage
// rejected.
func runSuiteStage(gateName, stage, repoRoot, root string, runner Runner, run SuiteRunner) GateResult {
	// The green cache: an identical worktree state already proven green under
	// this exact command (by a PostToolUse run or an earlier gate pass) is not
	// re-run. Red results are never cached, so a block always re-runs and
	// carries fresh output.
	key := ""
	if h := worktreeStateHash(root); h != "" {
		key = mechKey(root, h, runner)
	}
	if mechCacheHit(key) {
		line := fmt.Sprintf("gate %s: %s %s in %s → cache-hit", gateName, stage, cmdString(runner), root)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmdString(runner), "cache-hit", 0)
		return GateResult{}
	}
	restore := pinMechCargoTarget(runner, repoRoot)
	// Resolved while the gate's target dir is pinned: after restore() this
	// answers the OPERATOR's target, which is not the one the run contended
	// for and not the one whose owner names the holder.
	target := runnerTargetDir(runner, root)
	res, waited, acquired := runCargoLocked(run, runner, root, buildLockPrecommitDeadline, DefaultPrecommitTimeout)
	restore()
	logLockWait(gateName, root, runner, waited)
	if !acquired {
		// A commit the gate never tested must not land. This used to fail
		// open, and ten commits in one gate.log did exactly that: waited out
		// the full budget, logged queued-skipped, landed with zero tests
		// run. Rejecting is loud and recoverable (wait, or --no-verify
		// deliberately); failing open is silent and is not.
		line := fmt.Sprintf("gate %s: %s %s in %s → REJECTED (waited %.0fs, every build slot for %s is busy%s) — nothing was tested",
			gateName, stage, cmdString(runner), root, waited.Seconds(), target, buildLockHolderNote(target))
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmdString(runner), "queued-rejected", waited)
		return GateResult{Blocked: true, Message: queuedRejectMessage(runner, target, waited)}
	}
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	switch {
	case res.TimedOut:
		// A commit whose suite never finished is a commit nobody tested, and
		// unlike an edit-time timeout the consequence outlives the moment:
		// the untested code stays in history. The gate target is warm by the
		// time this fires, so the retry usually finishes.
		line := fmt.Sprintf("gate %s: %s %s in %s TIMEOUT after %.0fs REJECTED (nothing was tested)", gateName, stage, cmdString(runner), root, res.Duration.Seconds())
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmdString(runner), "timeout-rejected", res.Duration)
		return GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate %s: %s did not finish in %.0fs, so nothing was tested and the commit is refused. The gate target is now warm; retry the commit.",
			gateName, cmdString(runner), res.Duration.Seconds())}
	case !res.Passed:
		fmt.Fprintf(os.Stderr, "gate %s: %s %s in %s → blocked\n", gateName, stage, cmdString(runner), root)
		appendGateLog(gateName, root, cmdString(runner), blockedVerdict(stage, res.Output), res.Duration)
		return GateResult{Blocked: true, Message: mechRejectMessage(runner, res)}
	default:
		mechCacheAdd(key)
		line := mechGreenLine(gateName, stage, runner, root, res)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, root, cmdString(runner), "green", res.Duration)
	}
	return GateResult{}
}

// blockedVerdict is the gate.log word for a stage that failed. The
// whole-workspace check reads "check-rejected" because it is a compile
// verdict, not a test one — a reader scanning the log should not have to
// know which stage names mean "the code did not build".
func blockedVerdict(stage, output string) string {
	if stage != "check" {
		return stage + "-blocked"
	}
	// Two different problems with two different fixes: the tree does not
	// compile, or someone used a banned API. A reader scanning gate.log
	// should not have to open the output to tell them apart.
	if disallowedLintRe.MatchString(output) {
		return "lint-rejected"
	}
	return "check-rejected"
}

// disallowedLintRe recognises the two lints this stage denies, in either
// clippy spelling (the lint name uses underscores, the command-line note
// hyphens).
var disallowedLintRe = regexp.MustCompile(`disallowed[_-](?:method|type)s?|use of a disallowed (?:method|type)`)

// mechGreenLine composes the mechanical stage's green stderr line, sharing
// PostEdit's greenLabel renderer (passed count, or nextest's empty-crate
// exit-4 case) so the two call sites can't drift apart.
func mechGreenLine(gateName, stage string, r Runner, root string, res SuiteResult) string {
	return fmt.Sprintf("gate %s: %s %s in %s → %s", gateName, stage, cmdString(r), root, greenLabel(Green, res.Output, res.Duration))
}

// cargoOwnedFiles splits repo-root-relative files into those owned by SOME
// [package] Cargo.toml under root and those owned by none. Ownership is
// judged per file: a workspace-wide virtual manifest, or a path no member
// covers, never falls back to "test everything" — an unowned file is
// reported by the caller and excluded, not an excuse to run the whole
// workspace.
func cargoOwnedFiles(repoRoot, root string, filesRepoRel []string) (owned, unowned []string) {
	for _, f := range filesRepoRel {
		rel, err := filepath.Rel(root, filepath.Join(repoRoot, f))
		if err != nil {
			unowned = append(unowned, f)
			continue
		}
		if cargoPackageFor(root, filepath.ToSlash(rel)) == "" {
			unowned = append(unowned, f)
			continue
		}
		owned = append(owned, f)
	}
	return owned, unowned
}

// pinMechCargoTarget points the gate's cargo runs at the SAME target dir the
// developer builds into: CARGO_TARGET_DIR when the environment names one,
// else the repo's own <root>/target. One target per repo (the user's call,
// 2026-09-02): a private gate cache made every commit cold-compile what was
// already built next door, and on a Bevy-sized workspace that second copy
// cost a hundred gigabytes and ten minutes to prove the same thing twice.
// The per-target build lock is what keeps the two builds off each other's
// toes — the gate queues visibly like any other build.
func pinMechCargoTarget(r Runner, repoRoot string) func() {
	if r.Cmd != "cargo" {
		return func() {}
	}
	dir := resolvedDevTarget(repoRoot)
	if dir == "" {
		return func() {}
	}
	prev, had := os.LookupEnv("CARGO_TARGET_DIR")
	os.Setenv("CARGO_TARGET_DIR", dir)
	return func() {
		if had {
			os.Setenv("CARGO_TARGET_DIR", prev)
			return
		}
		os.Unsetenv("CARGO_TARGET_DIR")
	}
}

// resolvedDevTarget is where a build in repoRoot lands by default: the
// environment's CARGO_TARGET_DIR if set, else the workspace root's target/.
// The fail-first run must EXPORT this rather than inherit it — its worktree
// lives elsewhere, so cargo's default would silently create a second one.
func resolvedDevTarget(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	return ResolveCargoTargetDir(repoRoot)
}

// insideDir reports whether path lies lexically within base (inclusive).
// Windows compares case-insensitively — the same checkout routinely appears
// with both drive-letter casings.
func insideDir(base, path string) bool {
	base, path = filepath.Clean(base), filepath.Clean(path)
	if runtime.GOOS == "windows" {
		base, path = strings.ToLower(base), strings.ToLower(path)
	}
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// queuedRejectMessage composes the rejection for a commit the gate could
// not test because no build slot came free: it names the holder, so the
// operator knows what to wait for, and the two ways forward.
func queuedRejectMessage(r Runner, targetDir string, waited time.Duration) string {
	var b strings.Builder
	b.WriteString("TDD mechanical: NOTHING WAS TESTED \u2014 no build slot came free.\n")
	fmt.Fprintf(&b, "command: %s %s\n", r.Cmd, strings.Join(r.Args, " "))
	fmt.Fprintf(&b, "waited: %.0fs for a slot on %s\n", waited.Seconds(), targetDir)
	if o, ok := ReadBuildSlotOwner(targetDir); ok {
		fmt.Fprintf(&b, "holder: %q in %s (pid %d, held %s)\n", o.Cmd, o.Cwd, o.PID, time.Since(o.Started).Round(time.Second))
	}
	b.WriteString("Wait for that build to finish and commit again, raise APHROLLO_LOCK_WAIT_SECS, or commit with --no-verify if you mean to skip the gate.\n")
	return b.String()
}

// mechRejectMessage composes a mechanical block that says WHAT failed: the
// failing test names when the output parses, the runner-level error when it
// does not (a link error, a wedged harness), a pointer to the persisted FULL
// log, and a TAIL snippet — the failure detail of a long suite lives at the
// end, so head-truncation showed only green `ok` lines and made every real
// rejection look phantom.
func mechRejectMessage(r Runner, res SuiteResult) string {
	var b strings.Builder
	b.WriteString("TDD mechanical: tests failing — fix before committing.\n")
	fmt.Fprintf(&b, "command: %s %s\n", r.Cmd, strings.Join(r.Args, " "))
	if names := ExtractFailingTests(res.Output); len(names) > 0 {
		fmt.Fprintf(&b, "failing: %s\n", strings.Join(names, ", "))
	} else if res.Err != "" {
		fmt.Fprintf(&b, "no failing test parsed from output; runner error: %s\n", res.Err)
	}
	if path := mechRejectLogPath(); path != "" {
		if writeMechRejectLog(path, r, res) {
			fmt.Fprintf(&b, "full output: %s\n", path)
		}
	}
	b.WriteString(tailSnippet(res.Output))
	return b.String()
}

// mechRejectLogPath is where the last mechanical rejection's full output lives
// (one file, overwritten per rejection — the latest block is the one being
// debugged). "" when there is no state dir.
func mechRejectLogPath() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "mech-reject.log")
}

// writeMechRejectLog persists the full untruncated runner output with the
// command that produced it. Best-effort: a write failure only loses the
// pointer, never the block.
func writeMechRejectLog(path string, r Runner, res SuiteResult) bool {
	var b strings.Builder
	fmt.Fprintf(&b, "command: %s %s\nrunner error: %s\n\n", r.Cmd, strings.Join(r.Args, " "), res.Err)
	b.WriteString(res.Output)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	return os.WriteFile(path, []byte(b.String()), 0o600) == nil
}

// tailSnippet bounds runner output to its LAST maxSnippet chars — the mirror
// of snippet(): a suite's failure detail (the FAILED lines, the panic, the
// assertion diff) accumulates at the end of the run, so a bounded rejection
// must keep the tail and drop the head.
func tailSnippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxSnippet {
		return s
	}
	return "…[truncated]\n" + s[len(s)-maxSnippet:]
}

// suppressionCommitHeader prefixes a commit-time anti-cheat block; the policy's
// own reason (naming the directive and the fix) follows.
const suppressionCommitHeader = "TDD anti-cheat: this commit introduces a suppression that silences a quality gate."

// newSuppression scans the lines this commit ADDS for a suppression and, on the
// first hit in a source/test file, returns the block message. Only added lines
// are judged, so a directive that already lived in the file does not block an
// unrelated commit. The check masks each file's full staged post-image and then
// restricts to the added line numbers, so the masking sees balanced
// string/comment context and a crafted multi-line edit cannot hide a later
// added directive behind an unbalanced opener. Returns "" when nothing blocks.
func newSuppression(repoRoot string) string {
	for _, fa := range stagedAdds(repoRoot) {
		switch ClassifyFile(fa.path) {
		case Source, Test:
		default:
			continue
		}
		post, err := git(repoRoot, "show", ":"+fa.path)
		if err != nil {
			continue // file not in the index (e.g. deletion) → nothing to judge
		}
		v := addedView(post, fa.added, langOf(fa.path))
		if d := evaluateView(v, suppressionPolicies, commitPhase); d.Action == Block {
			return suppressionCommitHeader + "\n  " + fa.path + ": " + d.Reason
		}
	}
	return ""
}

// testDeclRes recognises an ADDED line that declares a test, per supported
// language. The set is deliberately declaration-shaped (attributes, test-func
// headers, subtest registrations) — an added assertion inside an existing test
// does not fire fail-first, matching its charter of proving NEW tests RED.
var testDeclRes = map[string][]*regexp.Regexp{
	".go": {
		regexp.MustCompile(`^\s*func\s+(Test|Benchmark|Fuzz|Example)\w*\s*\(`),
		regexp.MustCompile(`\bt\.Run\s*\(`),
	},
	".rs": {
		regexp.MustCompile(`^\s*#\[\s*(\w+(::\w+)*::)?(test|rstest|test_case)\b`),
	},
	".py": {
		regexp.MustCompile(`^\s*(async\s+)?def\s+test_`),
	},
	".zig": {
		regexp.MustCompile(`^\s*test\s+("|\{)`),
	},
	".js": jsTestDeclRes, ".jsx": jsTestDeclRes, ".mjs": jsTestDeclRes,
	".ts": jsTestDeclRes, ".tsx": jsTestDeclRes,
}

var jsTestDeclRes = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(it|test|describe)(\.\w+)?\s*\(`),
}

// stagedTestsAddDeclIn reports whether any of testFiles (a project root's OWN
// staged test files — repo-root-relative, matching stagedAdds' paths) has an
// added line carrying a test declaration. A test file in a language the
// table doesn't know errs toward true — fail-first then runs and, at worst,
// costs a suite, never a wrong verdict. Scoped to testFiles (not every staged
// test in the commit) so a multi-root commit's fail-first trigger for one
// root is never decided by a DIFFERENT root's test declarations.
func stagedTestsAddDeclIn(repoRoot string, testFiles []string) bool {
	if len(testFiles) == 0 {
		return false
	}
	want := make(map[string]bool, len(testFiles))
	for _, f := range testFiles {
		want[f] = true
	}
	for _, fa := range stagedAdds(repoRoot) {
		if !want[fa.path] || ClassifyFile(fa.path) != Test {
			continue
		}
		res, known := testDeclRes[strings.ToLower(filepath.Ext(fa.path))]
		if !known {
			return true
		}
		post, err := git(repoRoot, "show", ":"+fa.path)
		if err != nil {
			return true // can't read the post-image → judge conservatively
		}
		lines := strings.Split(post, "\n")
		for no := range fa.added {
			if no < 1 || no > len(lines) {
				continue
			}
			for _, re := range res {
				if re.MatchString(lines[no-1]) {
					return true
				}
			}
		}
	}
	return false
}

// fileAdd is the set of added line numbers (1-based, in the post-image) for one
// file in a staged diff.
type fileAdd struct {
	path  string
	added map[int]bool
}

// stagedAdds parses `git diff --cached -U0` into the added line NUMBERS per
// file, keyed by the `+++ b/<path>` header and skipping deletions
// (`+++ /dev/null`). Line numbers come from each hunk's `@@ … +start[,count] @@`
// new-side range, advanced as `+` lines are consumed, so the caller can mask the
// full post-image and judge only these lines — far more robust than the old
// approach of masking the deletion-stripped added-line text on its own.
func stagedAdds(repoRoot string) []fileAdd {
	out, err := git(repoRoot, "diff", "--cached", "--unified=0", "--no-color")
	if err != nil {
		return nil
	}
	var (
		adds  []fileAdd
		cur   string
		lines map[int]bool
		newNo int // next new-file line number within the current hunk
	)
	flush := func() {
		if cur != "" && len(lines) > 0 {
			adds = append(adds, fileAdd{path: cur, added: lines})
		}
		lines = nil
	}
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			lines = map[int]bool{}
		case strings.HasPrefix(line, "+++"): // +++ /dev/null (deletion)
			flush()
			cur = ""
		case strings.HasPrefix(line, "@@"):
			newNo = hunkNewStart(line)
		case strings.HasPrefix(line, "+"):
			if lines != nil && newNo > 0 {
				lines[newNo] = true
				newNo++
			}
		case strings.HasPrefix(line, "-"):
			// deletions don't advance the new-file line counter
		default:
			// context line (none at -U0) advances the new-file counter
			if newNo > 0 {
				newNo++
			}
		}
	}
	flush()
	return adds
}

// hunkNewStartRe captures the new-file start line from a `@@ -a,b +c,d @@`
// header — the digits right after the `+`, before any `,count`.
var hunkNewStartRe = regexp.MustCompile(`\+(\d+)`)

// hunkNewStart returns the new-file starting line of a `@@ -a,b +c,d @@` header,
// or 0 if it can't be parsed.
func hunkNewStart(header string) int {
	m := hunkNewStartRe.FindStringSubmatch(header)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// splitKinds partitions repo-relative staged paths into test and source files,
// ignoring everything else.
func splitKinds(paths []string) (tests, srcs []string) {
	for _, p := range paths {
		switch ClassifyFile(p) {
		case Test:
			tests = append(tests, p)
		case Source:
			srcs = append(srcs, p)
		}
	}
	return tests, srcs
}

// failFirstViolated builds a throwaway worktree at HEAD, applies ONLY the
// staged test changes, and runs the suite there. It returns (violated,
// conclusive): violated is true when the tests pass without the new source
// (they should fail first); conclusive is false when the check could not run,
// in which case the caller must not block. This is failFirstViolatedAt with
// root == repoRoot, kept as its own name for the (still common) single-root
// case and for direct callers/tests.
func failFirstViolated(repoRoot string, tests []string, run SuiteRunner) (violated, conclusive bool, dur time.Duration) {
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
func failFirstViolatedAt(repoRoot, root string, tests []string, run SuiteRunner) (violated, conclusive bool, dur time.Duration) {
	wt := failFirstWorktreeDir(repoRoot)
	if wt == "" {
		var err error
		if wt, err = os.MkdirTemp("", "gate-failfirst-"); err != nil {
			return false, false, 0
		}
	} else {
		// Stable per-repo path: a leftover registration from a crashed run
		// must go before `worktree add` will accept the path again.
		_, _ = git(repoRoot, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
	}
	defer os.RemoveAll(wt)
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		return false, false, 0
	}
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", wt) }() // best-effort cleanup

	// The staged test diff applied onto HEAD: tests present, new source absent.
	diff, err := gitStaged(repoRoot, tests)
	if err != nil || strings.TrimSpace(diff) == "" {
		return false, false, 0
	}
	if err := gitApply(wt, diff); err != nil {
		return false, false, 0 // can't reproduce the test state → don't block
	}

	execRoot, err := execRootIn(wt, repoRoot, root)
	if err != nil {
		return false, false, 0
	}
	runner, ok := DetectRunner(execRoot)
	if !ok {
		return false, false, 0
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
			return false, false, 0
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
	if !acquired {
		return false, false, 0 // another cargo build holds the machine lock — no verdict either way
	}
	if res.TimedOut {
		return false, false, res.Duration // a killed run reaches no verdict either way
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
	return res.Passed, true, res.Duration
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

// --- git plumbing (scrubbed environment) ------------------------------------

// cleanGitEnv strips every GIT_* variable from the environment. A git hook runs
// with GIT_DIR / GIT_INDEX_FILE / GIT_WORK_TREE / GIT_OBJECT_DIRECTORY and
// friends pointing at the OUTER repo; leaking any of them makes worktree
// commands operate on the wrong state. An allowlist (drop all GIT_*) is safer
// than blocklisting the few that were known to cause trouble.
func cleanGitEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	// Belt and braces (task A11): mark every git subprocess aphrollo itself
	// spawns as already-queued, so if one of these (worktree add/remove,
	// apply, diff --cached, rev-parse, ...) happens to route back through
	// the `tdd git` shim via PATH, it passes straight through instead of
	// waiting on the per-repo git lock its own parent process holds.
	out = append(out, GitQueuedEnv+"=1")
	return out
}

func git(dir string, args ...string) (string, error) {
	return gitStdin(dir, nil, args...)
}

// gitStdin runs git in dir with a scrubbed environment, optionally feeding stdin
// (nil for none), and returns the combined output. It is the single place the
// exec/clean-env/CombinedOutput pattern lives.
func gitStdin(dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	cmd.Stdin = stdin
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mergeInProgressRefs is checked in order: the first of these refs that
// resolves is what Precommit reports and dispatches on. MERGE_HEAD covers a
// conflicted `git merge`; CHERRY_PICK_HEAD and REVERT_HEAD cover the
// identical situation for a conflicted `git cherry-pick`/`git revert` — all
// three fire git's pre-commit hook (not pre-merge-commit) when concluded
// with a manual `git commit`, and none of them should be judged by
// fail-first against the whole resulting diff.
var mergeInProgressRefs = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"}

// mergeInProgressRef reports which of mergeInProgressRefs currently
// resolves in repoRoot (via `git rev-parse -q --verify <ref>`, which exits
// 0 only when the ref both exists and names a valid object), or "" if none
// does.
func mergeInProgressRef(repoRoot string) string {
	for _, ref := range mergeInProgressRefs {
		if _, err := git(repoRoot, "rev-parse", "-q", "--verify", ref); err == nil {
			return ref
		}
	}
	return ""
}

// stagedDiffFilter is what the gate considers a staged CHANGE: added, copied,
// modified, renamed or type-changed. R and T were missing, so a refactor
// commit — a rename plus an edit, which git records as R — produced zero gate
// activity for every renamed file it carried.
const stagedDiffFilter = "ACMRT"

// stagedFiles lists the changed paths in the index, as repo-root-relative
// paths. `-M` turns rename detection on explicitly (never inherited from the
// repo's diff.renames), and `--name-only` prints a rename's DESTINATION — the
// path that exists after the commit and the only one worth testing.
func stagedFiles(repoRoot string) []string {
	out, err := git(repoRoot, "diff", "--cached", "--name-only", "-M", "--diff-filter="+stagedDiffFilter)
	if err != nil {
		return nil
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// gitStaged returns the staged diff restricted to the given pathspecs.
func gitStaged(repoRoot string, paths []string) (string, error) {
	args := append([]string{"diff", "--cached", "--"}, paths...)
	return git(repoRoot, args...)
}

// gitApply applies a unified diff to a worktree via `git apply` on stdin.
func gitApply(wt, diff string) error {
	if out, err := gitStdin(wt, strings.NewReader(diff), "apply", "--whitespace=nowarn"); err != nil {
		return fmt.Errorf("git apply: %s", strings.TrimSpace(out))
	}
	return nil
}

// RepoRoot returns the git top-level for dir, or "" if dir is not in a repo —
// the working directory a git pre-commit hook should evaluate.
func RepoRoot(dir string) string {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(out))
}
