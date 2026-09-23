package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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
		line := fmt.Sprintf("[%s] gate %s: %s in %s → cache-hit", stage, gateName, cmdString(runner), root)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmdString(runner), "cache-hit", 0)
		return GateResult{}
	}
	restore := pinMechCargoTarget(runner, repoRoot)
	// Resolved while the gate's target dir is pinned: after restore() this
	// answers the OPERATOR's target, which is not the one the run contended
	// for and not the one whose owner names the holder.
	target := runnerTargetDir(runner, root)
	// What this same stage running this same command is recorded to need.
	// The build-slot wait carves out of the stage budget, which on a busy
	// box handed one suite 96s of a 600s budget for work that takes ~350s —
	// the load may shrink the budget down to this floor and no further
	// (issue #660, budgetfloor.go). Zero when gate.log has no completed run
	// to derive one from, which is today's arithmetic unchanged.
	floor := recordedSuiteFloor(gateName, cmdString(runner))
	res, waited, acquired := runCargoLocked(run, runner, root, precommitLockWait(), DefaultPrecommitTimeout, floor.Budget)
	restore()
	logLockWait(gateName, root, runner, waited)
	if !acquired {
		// A commit the gate never tested must not land. This used to fail
		// open, and ten commits in one gate.log did exactly that: waited out
		// the full budget, logged queued-skipped, landed with zero tests
		// run. Rejecting is loud and recoverable (wait, or --no-verify
		// deliberately); failing open is silent and is not.
		line := fmt.Sprintf("[%s] gate %s: %s in %s → REJECTED (waited %.0fs, every build slot for %s is busy%s) — nothing was tested",
			stage, gateName, cmdString(runner), root, waited.Seconds(), target, buildLockHolderNote(target))
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmdString(runner), "queued-rejected", waited)
		return GateResult{Blocked: true, Message: queuedRejectMessage(runner, target, waited)}
	}
	if treatAsEmptyPass(res) {
		res.Passed = true
	}
	// A run that exited 0 having executed zero tests (the #194 shape for Go:
	// a TestMain that returns or calls os.Exit(0) before m.Run(); its cargo,
	// pytest and vitest counterparts in vacuous_dispatch.go) is not a pass —
	// judged from the run's own output, since a sibling package/target's real
	// tests passing must never hide another going quietly vacuous beside it.
	// Checked before the switch below so it never falls into the ordinary
	// green case.
	if res.Passed && !res.TimedOut {
		names, err := vacuousNames(runner, res)
		if err != nil {
			// The run exited 0, but this gate could not read what it
			// actually tested — the same "nothing was proven" shape as any
			// other check-error, not a clean pass (#317: an unmeasured run
			// must never read as one).
			return verdictFor(gateName, stage, root, cmdString(runner), stageOutcome{
				kind: outcomeCheckError,
				err:  err,
				message: fmt.Sprintf(
					"gate %s: %s → REJECTED (%v)\n  the commit cannot be judged against a test-result stream this gate could not read",
					gateName, cmdString(runner), err),
			})
		}
		if len(names) > 0 {
			return verdictFor(gateName, stage, root, cmdString(runner), stageOutcome{
				kind:   outcomeVacuous,
				result: res,
				message: fmt.Sprintf(
					"gate %s: %s executed zero tests in %s despite exiting 0, so nothing was tested there and the commit is refused.",
					gateName, cmdString(runner), strings.Join(names, ", ")),
			})
		}
	}
	switch {
	case res.TimedOut:
		// A commit whose suite never finished is a commit nobody tested, and
		// unlike an edit-time timeout the consequence outlives the moment:
		// the untested code stays in history.
		//
		// What the refusal may NOT say is "retry, the target is warm now":
		// under sustained load the next attempt queues longer and gets a
		// smaller budget, so that advice sent four attempts at one suite
		// into four full runs that proved nothing (#660). It names the
		// floor the run actually had and the record that set it instead, so
		// a reader can tell "this box is too busy" from "this suite outgrew
		// its budget".
		//
		// A timeout is the one verdict where the code under test may be
		// entirely innocent (#526): sampled once, here, never on a green
		// run, so the reader can tell "this suite got slower" from
		// "something unrelated ate the cores" without reasoning about it
		// from the log alone.
		load := foreignLoadReport(os.Getpid())
		line := fmt.Sprintf("[%s] gate %s: %s in %s TIMEOUT after %.0fs REJECTED (nothing was tested)\n%s", stage, gateName, cmdString(runner), root, res.Duration.Seconds(), load)
		fmt.Fprintln(os.Stderr, line)
		AppendGateLog(gateName, root, cmdString(runner), "timeout-rejected", res.Duration)
		return GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate %s: %s did not finish in %.0fs, so nothing was tested and the commit is refused.\n%s\n%s",
			gateName, cmdString(runner), res.Duration.Seconds(), floor.RefusalNote(DefaultPrecommitTimeout), load)}
	case !res.Passed:
		fmt.Fprintf(os.Stderr, "[%s] gate %s: %s in %s → blocked\n", stage, gateName, cmdString(runner), root)
		logSuiteVerdict(gateName, root, cmdString(runner), blockedVerdict(stage, res.Output), res)
		return GateResult{Blocked: true, Message: mechRejectMessage(runner, res)}
	default:
		mechCacheAdd(key)
		// A suite RAN and passed. That, and not a cache hit, is what the
		// gate note claims to CI — see noteSuiteGreen. What it proved is
		// bounded by the ground this command covered: suiteProof.note takes
		// the scope, and the note is written only if it covers what the
		// commit owed (suiteproof.go).
		noteSuiteGreen()
		suiteProof.Note(runner, res)
		line := mechResultLine(gateName, stage, runner, root, res)
		fmt.Fprintln(os.Stderr, line)
		logSuiteVerdict(gateName, root, cmdString(runner), stageSuiteVerdict(runner, res), res)
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

// mechResultLine composes the stderr line for a stage run that did not
// block. A run that actually executed tests shares PostEdit's greenLabel
// renderer, so the two call sites cannot drift apart; a run that executed
// NONE says so instead. A merge is measured, not certified, and "green (0
// tests — nothing to run)" at the commit and merge gates was the same false
// green as at edit time, at the point where it carries the most weight.
// LABEL ONLY: the stage still passes such a run, exactly as before — a crate
// with no test target still lands, and the line says so rather than leaving
// a reader to wonder whether it was refused.
func mechResultLine(gateName, stage string, r Runner, root string, res SuiteResult) string {
	label := greenLabel(Green, res.Output, res.Duration)
	if v := untestedVerdict(r, res); v != "" {
		label = fmt.Sprintf("%s (%.1fs) — nothing was tested here; not a refusal (a target with no reachable test still lands), and not a green",
			strings.ToUpper(v), res.Duration.Seconds())
	}
	return fmt.Sprintf("[%s] gate %s: %s in %s → %s", stage, gateName, cmdString(r), root, label)
}

// stageSuiteVerdict is the gate.log word for that same run: "green" when
// tests actually ran, and the inconclusive name when none did — which keeps
// it out of isSettledVerdict's family, so a run that measured nothing cannot
// arm the 30-minute block over the hand-run that would measure something.
func stageSuiteVerdict(r Runner, res SuiteResult) string {
	if v := untestedVerdict(r, res); v != "" {
		return v
	}
	return "green"
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
	dir := StateDir()
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
