package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// postToolUseInput is the subset of the PostToolUse payload the RED/GREEN
// engine needs.
type postToolUseInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	ToolResponse struct {
		// Success is a pointer so a missing field (unknown) is distinct from an
		// explicit false (the tool itself failed — nothing to test).
		Success *bool `json:"success"`
	} `json:"tool_response"`
}

var gatedPostTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true}

// PostEdit runs the relevant tests after an edit and returns the advisory text
// to surface to the model. It reports LOUDLY for every run that actually
// resolves to a verdict — green, writing-test, no-delta, or red — and for
// every run that DIDN'T (timeout, streak skip): a bare "" only ever means the
// hook found nothing to test (non-edit tool, no path, no project, no runner,
// gate turned off), never "it ran and passed silently" — a session must be
// able to tell "green" from "never ran" without re-reading the transcript.
// PostToolUse never blocks and never errors out regardless.
func PostEdit(raw []byte, run SuiteRunner) string {
	var in postToolUseInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	text := ""
	failed := in.ToolResponse.Success != nil && !*in.ToolResponse.Success
	if gatedPostTools[in.ToolName] && in.ToolInput.FilePath != "" && !failed {
		text, _ = postEditFile(in.SessionID, in.ToolInput.FilePath, run)
	}
	return withSessionHarvest(text, in.SessionID)
}

// postEditFile is the whole post-edit path for ONE changed file — the body
// PostEdit used to be, lifted out so the Bash hook can put a file a shell
// command rewrote through the identical path. It reports whether it left a
// build running, which is what bounds a Bash call to one deferral.
func postEditFile(session, target string, run SuiteRunner) (string, bool) {
	kind := ClassifyFile(target)
	if kind == Ignore {
		return "", false
	}
	root := FindProjectRoot(target)
	if root == "" {
		return "", false
	}

	// Before the enforcement check: an edit with the gate off still changed the file.
	editID := recordEdit(root, target)
	snap, ok := captureStateSnapshot(session, target, root)
	if !ok {
		return "", false
	}
	snap.editID = editID

	// A narrowed cargo run whose package-scope form already proved green at
	// this exact worktree state has nothing left to ask — most often the
	// precommit/premerge stage just ran and cached that crate's full suite
	// (issue #715: a module-filtered edit-time run then found zero and
	// claimed "the code was NOT tested", two lines after it just had been).
	if line := cargoFullSuiteAlreadyGreenLine(snap.runner, root); line != "" {
		return line, false
	}

	headSHA := ""
	if snap.fingerprint != nil {
		headSHA = snap.fingerprint.HeadSHA
	}
	// Timeout backoff: a heavy crate (Bevy client/server) can blow the
	// edit-time budget on nearly every edit, burning the full timeout for a
	// discarded result. Two consecutive timeouts AT THE SAME head SHA earn a
	// SKIPPED line instead of a real run — the suite is skipped entirely —
	// until a commit moves HEAD and re-arms a fresh budget (stampTimeout
	// starts the streak over on any SHA change).
	if snap.state != nil {
		if ps, exists := snap.state.ByProject[root]; exists && ps.TimeoutStreak >= 2 && ps.TimeoutSHA == headSHA {
			AppendGateLog("postedit", root, "", "skipped", 0)
			return streakSkipAdvisory(root), false
		}
	}

	if deferPhases.Load() {
		return postEditDeferred(snap, root, target, headSHA, session)
	}

	res, terminal := runPostEditSuite(run, snap, root, headSHA, DefaultPostEditTimeout)
	if terminal != "" {
		return terminal, false
	}
	// A compile check ends here: an --example or --bench target ran no test,
	// so there is no verdict to classify and nothing to widen into.
	if line := buildOnlyTerminal(snap.runner, root, res); line != "" {
		return line, false
	}
	// A nested tests/<dir>/ file no `mod` declaration reaches also ends here:
	// the run that just passed never built it at all, so it is not evidence
	// about this edit either, whatever else in the package it exercised.
	if line := notCompiledTerminal(snap.runner, root, target, res); line != "" {
		return line, false
	}
	widenNote := ""
	// A NARROWED run that selected nothing has not judged the code: the
	// crate's tests may simply live where the filter did not look. Widen
	// once and let that run answer; only a selection that stays empty is
	// reported, as an inconclusive rather than a green.
	if postEditSelectedZero(snap.runner, res) {
		empty := resolveEmptySelection(run, snap, root, headSHA, res)
		if empty.terminal != "" {
			return empty.terminal, false
		}
		snap.runner, res, widenNote = empty.runner, empty.res, empty.note
	}
	// Same posture as a timeout: a build that failed to link a crate this
	// edit did not touch reached no verdict about the edit, so the last real
	// outcome stays authoritative and state is left untouched (issue #593).
	if line := foreignBuildAdvisory(root, target, cmdString(snap.runner), res); line != "" {
		return line, false
	}
	outcome := classifyRunOutcome(snap.runner, root, res, snap.prevFailing)
	failing := ExtractFailingTests(res.Output)
	passed, hasCount := parsePassedCount(res.Output)
	unconstrained := unconstrainedGreen(kind, outcome, snap, root, passed, hasCount)

	if snap.state != nil {
		// Recorded as GREEN even when the advisory says unconstrained: the
		// note is about coverage, not about failure, and /tdd status must not
		// read it as something to fix.
		stamped := projectState{
			Outcome:      string(outcome),
			FailingTests: failing,
			Runner:       append([]string{snap.runner.Cmd}, snap.runner.Args...),
			Fingerprint:  snap.fingerprint,
		}
		if hasCount && outcome == Green {
			stamped.PassedCount = passed
		}
		snap.state.Stamp(root, stamped)
		_ = snap.state.Save(snap.statePath)
	}

	// Seed the mechanical green cache: when this exact command is what the
	// commit gate would run over the same worktree state (a full-suite runner
	// like cargo/pytest/zig, or a matching scoped run), the gate skips the
	// re-run entirely.
	if res.Passed {
		if h := worktreeStateHash(root); h != "" {
			mechCacheAdd(mechKey(root, h, snap.runner))
		}
	}

	logSuiteVerdict("postedit", root, cmdString(snap.runner), string(outcome), res)
	recordEditVerdict(root, snap.editID, cmdString(snap.runner), outcome, res.Output)
	if outcome.IsRed() {
		return withNote(redSummary(snap.runner, root, outcome, res.Output), widenNote), false
	}
	if unconstrained {
		return withNote(unconstrainedLine(snap.runner, root, passed, res.Duration), widenNote), false
	}
	return withNote(passAdvisory(snap.runner, root, outcome, res.Output, res.Duration, snap.prevFailing), widenNote), false
}

// stateSnapshot is the per-edit state plumbing PostEdit needs to run the suite
// and stamp the outcome: the loaded session (nil when there is no session id),
// where it persists, the narrowed runner, the git fingerprint at this edit, and
// the previously-recorded failing set under that fingerprint.
type stateSnapshot struct {
	state       *sessionState
	statePath   string
	runner      Runner
	fingerprint *fingerprint
	prevFailing []string
	// editID names this edit's edit-ledger record, where its verdict lands.
	editID string
}

// captureStateSnapshot loads the session, resolves the narrowed runner for the
// edited target, and computes the git fingerprint and prior failing set. It
// reports false when the edit cannot be tested — enforcement is off for this
// session, or the project uses a toolchain the gates don't know — so PostEdit
// stays silent.
func captureStateSnapshot(session, target, root string) (stateSnapshot, bool) {
	state, statePath := loadSession(session)
	if state != nil && state.Overrides.Off {
		return stateSnapshot{}, false
	}
	runner, ok := DetectRunner(root)
	if !ok {
		return stateSnapshot{}, false
	}
	runner = NarrowToRelatedTests(runner, target, root)

	fp := computeFingerprint(root)
	var prevFailing []string
	if state != nil {
		prevFailing = state.PrevFailing(root, fp)
	}
	return stateSnapshot{
		state:       state,
		statePath:   statePath,
		runner:      runner,
		fingerprint: fp,
		prevFailing: prevFailing,
	}, true
}

// passedCountRes recognises a runner's own "N passed" summary line, so a
// green advisory can report the count without re-parsing test output beyond
// what the runner already prints. Unrecognised formats (vitest/jest/pytest/
// zig) just omit the count — never a guess.
var passedCountRes = []*regexp.Regexp{
	regexp.MustCompile(`test result: ok\.\s*(\d+) passed`),                                    // go test
	regexp.MustCompile(`(?m)^\s*Summary\s*\[[^\]]*\]\s*\d+\s*tests?\s*run:\s*(\d+)\s*passed`), // cargo nextest
}

// parsePassedCount extracts the passed-test count from runner output, when
// the format is recognized.
func parsePassedCount(output string) (int, bool) {
	for _, re := range passedCountRes {
		if m := re.FindStringSubmatch(output); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n, true
			}
		}
	}
	return goPassedCount(output)
}

// treatAsEmptyPass reports whether a non-passing, non-timed-out SuiteResult
// is actually nextest's "no tests to run" exit-4 case, which every consumer
// (PostEdit, Precommit's mechanical stage) must treat as an empty PASS rather
// than a failure. Callers flip res.Passed = true on a true result before
// doing anything else with it.
func treatAsEmptyPass(res SuiteResult) bool {
	return !res.Passed && !res.TimedOut && noTestsToRunRe.MatchString(res.Output)
}

// greenLabel renders the "<outcome-ish> (...)" suffix shared by PostEdit's
// advisory and Precommit's mechanical stderr line for a run that is a PASS —
// extracted so the two call sites render identically and can't drift apart.
// The word is the outcome's own: a run classified writing-test tested
// nothing and says so, and never borrows green.
func greenLabel(outcome Outcome, output string, dur time.Duration) string {
	if outcome == WritingTest {
		return fmt.Sprintf("%s (0 tests ran, %.1fs — nothing was tested)", outcome, dur.Seconds())
	}
	if n, ok := parsePassedCount(output); ok {
		return fmt.Sprintf("%s (%d passed, %.1fs)", outcome, n, dur.Seconds())
	}
	return fmt.Sprintf("%s (%.1fs)", outcome, dur.Seconds())
}

// passAdvisory composes the one-line advisory for a run that resolved to a
// NON-red outcome (green, green-with-warnings, writing-test, no-delta) — the
// PostEdit contract is loud on every run, so these are no longer silent. The
// passed count is included when the runner's own output states it plainly;
// otherwise the line still reports the outcome and the duration. A no-delta
// run additionally names WHICH test is still failing (task A8) — no-delta
// means "still red, but nothing NEW", and a bare "no-delta" line leaves the
// session guessing which pre-existing failure it is.
func passAdvisory(r Runner, root string, outcome Outcome, output string, dur time.Duration, prevFailing []string) string {
	line := fmt.Sprintf("gate: %s in %s → %s", cmdString(r), root, greenLabel(outcome, output, dur))
	if outcome == NoDelta {
		if hint := noDeltaStillFailingLine(output, prevFailing); hint != "" {
			line += "\n" + hint
		}
	}
	return line
}

// noDeltaStillFailingLine names the still-failing test for a no-delta run:
// the current run's own output normally already parses a name (that is what
// makes it no-delta at all — ClassifyOutcome's noNewFailures guard rejects
// an empty current-failing set), so that name wins. The previously-recorded
// failing set is a DEFENSIVE fallback only, for if that guard ever loosens —
// "" when neither source has a name.
func noDeltaStillFailingLine(output string, prevFailing []string) string {
	if first := firstFailingName(output); first != "" {
		return "first failure: " + first
	}
	if len(prevFailing) > 0 {
		return "still failing: " + prevFailing[0]
	}
	return ""
}

// redSummary composes the actionable message for a RED run: the headline, the
// first failing test, the guidance for that outcome, and a bounded snippet of
// the runner output.
func redSummary(r Runner, root string, outcome Outcome, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gate: %s %s in %s → outcome=%s", r.Cmd, strings.Join(r.Args, " "), root, outcome)
	if first := firstFailingName(output); first != "" {
		fmt.Fprintf(&b, "\nfirst failure: %s", first)
	}
	if g := guidance(outcome); g != "" {
		fmt.Fprintf(&b, "\n%s", g)
	}
	// An ADDITIONAL line, never a softer verdict (issue #651): when the
	// compiler denies a symbol the crate's own sources declare AND that
	// crate's artifacts are shared with another checkout, say which
	// fingerprint to remove by hand. Silent in every other case, which is
	// nearly all of them.
	if hint := staleArtifactHint(runnerDir(r, root), output); hint != "" {
		fmt.Fprintf(&b, "\n%s", hint)
	}
	if path := postEditRedLogPath(); path != "" {
		if writePostEditRedLog(path, output) {
			fmt.Fprintf(&b, "\nfull output: %s", path)
		}
	}
	// A real test FAILURE (ExtractFailingTests found a name) puts its detail
	// at the END of the output — nextest's FAIL summary line and cargo
	// test's own FAILED dump both accumulate there, same as the mechanical
	// gate's tailSnippet already accounts for. A head-only snippet showed
	// the green PASS lines first and truncated BEFORE ever reaching the
	// failure, matching the reported bug: "hook reports red/no-delta but
	// truncates before naming the test". Anything else (a compile error,
	// red-missing-impl, red-bogus) puts its actionable line at the START,
	// so the head snippet is still correct there.
	body := snippet(output)
	if len(ExtractFailingTests(output)) > 0 {
		body = tailSnippet(output)
	}
	fmt.Fprintf(&b, "\n```\n%s\n```", body)
	return b.String()
}

// postEditRedLogPath is where the last RED PostEdit run's FULL, untruncated
// output lives (one file, overwritten per RED — the latest block is the one
// being debugged), mirroring mechRejectLogPath's contract for the
// commit-time gate. "" when there is no state dir.
func postEditRedLogPath() string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "postedit-red.log")
}

// writePostEditRedLog persists the full untruncated RED output verbatim.
// Best-effort: a write failure only loses the pointer, never the advisory.
func writePostEditRedLog(path, output string) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	return os.WriteFile(path, []byte(output), 0o600) == nil
}

// guidance maps a RED outcome to a one-line next step.
func guidance(o Outcome) string {
	switch o {
	case RedMissingImpl:
		return "✓ Clean RED — the symbol under test is undefined. Write the minimum implementation."
	case RedBogus:
		return "✗ Test setup is broken (syntax/import/collection). Fix the test before the implementation."
	default:
		return "✗ Tests failing. Make them green before moving on."
	}
}

// DefaultPostEditTimeout is the ONE foreground budget an edit gets, covering
// the build and run phases TOGETHER: whichever is still going when it expires
// keeps running detached and reports at the next hook. It is the canonical
// PostToolUse budget —
// the single source of truth for cli.go's postEditTimeout AND init.go's
// PostToolUse hook-template timeout, so the two can never silently drift
// apart again. They did: the harness template stayed at 90s after this
// Go-side budget was bumped to 100s (task A2), so Claude Code killed the
// hook PROCESS from OUTSIDE before RunSuite's own context deadline ever
// fired — no TIMEOUT line, no state stamped, and the spawned cargo process
// left orphaned (the harness's kill reaches only the direct hook process,
// never RunSuite's own WaitDelay-based child cleanup, which needs its OWN
// deadline to actually fire first).
const DefaultPostEditTimeout = 110 * time.Second

const maxSnippet = 2000

// snippet bounds runner output so a huge failure dump doesn't flood context.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxSnippet {
		return s
	}
	return s[:maxSnippet] + "\n…[truncated]"
}

// firstFailingName returns the first failing test name in runner output, so a
// RED summary can name the failure inline without the model scrolling output.
func firstFailingName(output string) string {
	if names := ExtractFailingTests(output); len(names) > 0 {
		return names[0]
	}
	return ""
}

// postToolUseOutput mirrors the PostToolUse hook output contract.
type postToolUseOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// RenderPostToolUse turns advisory text into the hook payload. PostToolUse
// never blocks, so the exit code is always 0; empty text is silent.
func RenderPostToolUse(text string) ([]byte, int) {
	if text == "" {
		return nil, 0
	}
	var out postToolUseOutput
	out.HookSpecificOutput.HookEventName = "PostToolUse"
	out.HookSpecificOutput.AdditionalContext = text
	b, _ := json.Marshal(out)
	return b, 0
}
