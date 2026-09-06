package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	// A Go run that exited 0 having executed zero tests IN SOME PACKAGE (the
	// #194 shape: a TestMain that returns or calls os.Exit(0) before
	// m.Run()) is not a pass — judged per package via the run's own -json
	// stream, since a sibling package's real tests passing must never hide
	// another package going quietly vacuous beside them. Checked before the
	// switch below so it never falls into the ordinary green case.
	if res.Passed && !res.TimedOut && runner.Cmd == "go" {
		pkgs, err := vacuousGoPackages(res.GoTestJSON)
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
		if len(pkgs) > 0 {
			return verdictFor(gateName, stage, root, cmdString(runner), stageOutcome{
				kind:   outcomeVacuous,
				result: res,
				message: fmt.Sprintf(
					"gate %s: %s executed zero tests in %s despite exiting 0, so nothing was tested there and the commit is refused.",
					gateName, cmdString(runner), strings.Join(pkgs, ", ")),
			})
		}
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
		// A suite RAN and passed. That, and not a cache hit, is what the
		// gate note claims to CI — see noteSuiteGreen.
		noteSuiteGreen()
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
