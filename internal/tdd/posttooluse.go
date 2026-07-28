package tdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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

// SuiteResult is the outcome of executing a Runner.
type SuiteResult struct {
	Passed bool
	Output string
	// TimedOut marks a run killed at its deadline. A timeout says nothing
	// about the code under test, so every consumer treats it as inconclusive
	// — PostEdit stays silent, the mechanical gate does not block, fail-first
	// reaches no verdict. Without the signal a slow suite reads as RED and
	// the gates nag or block over a stopwatch, not a failure.
	TimedOut bool
	// Err is the runner-level error text ("" when the run completed cleanly):
	// an exit status, a spawn failure, or Go's wait-delay note. It is what the
	// mechanical gate surfaces when the output itself names no failing test —
	// a rejection must say WHAT failed, not just "failing".
	Err string
}

// SuiteRunner executes a runner in a project root. It is injected so the
// orchestration can be tested without spawning real test suites.
type SuiteRunner func(r Runner, root string) SuiteResult

var gatedPostTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true}

// PostEdit runs the relevant tests after an edit and returns the advisory text
// to surface to the model — empty when there is nothing actionable to say. It
// is SILENT unless the run is RED: a green/scaffolding/no-delta run still
// updates state (for the next delta) but adds no noise to the model's context.
// Any reason it cannot run (non-edit tool, no path, no project, no runner,
// gate turned off) yields "" — PostToolUse never blocks and never errors out.
func PostEdit(raw []byte, run SuiteRunner) string {
	var in postToolUseInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	if !gatedPostTools[in.ToolName] || in.ToolInput.FilePath == "" {
		return ""
	}
	if in.ToolResponse.Success != nil && !*in.ToolResponse.Success {
		return ""
	}
	target := in.ToolInput.FilePath
	kind := ClassifyFile(target)
	if kind == Ignore {
		return ""
	}
	root := FindProjectRoot(target)
	if root == "" {
		return ""
	}

	snap, ok := captureStateSnapshot(in.SessionID, target, root)
	if !ok {
		return ""
	}

	res := run(snap.runner, root)
	if res.TimedOut {
		// A killed run proves nothing — no advisory, no state stamp: the last
		// real outcome stays authoritative for the next delta.
		return ""
	}
	outcome := ClassifyOutcome(res.Passed, res.Output, snap.prevFailing)
	failing := ExtractFailingTests(res.Output)

	if snap.state != nil {
		snap.state.stamp(root, projectState{
			Outcome:      string(outcome),
			FailingTests: failing,
			Runner:       append([]string{snap.runner.Cmd}, snap.runner.Args...),
			Fingerprint:  snap.fingerprint,
		})
		_ = snap.state.save(snap.statePath)
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

	if !outcome.IsRed() {
		return ""
	}
	return redSummary(snap.runner, root, outcome, res.Output)
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
		prevFailing = state.prevFailing(root, fp)
	}
	return stateSnapshot{
		state:       state,
		statePath:   statePath,
		runner:      runner,
		fingerprint: fp,
		prevFailing: prevFailing,
	}, true
}

// redSummary composes the actionable message for a RED run: the headline, the
// first failing test, the guidance for that outcome, and a bounded snippet of
// the runner output.
func redSummary(r Runner, root string, outcome Outcome, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tdd: %s %s in %s → outcome=%s", r.Cmd, strings.Join(r.Args, " "), root, outcome)
	if first := firstFailingName(output); first != "" {
		fmt.Fprintf(&b, "\nfirst failure: %s", first)
	}
	if g := guidance(outcome); g != "" {
		fmt.Fprintf(&b, "\n%s", g)
	}
	fmt.Fprintf(&b, "\n```\n%s\n```", snippet(output))
	return b.String()
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

// RunSuite is the production SuiteRunner: it executes the test command with a
// bounded timeout and a quiet, deterministic environment (CI=1, NO_COLOR=1),
// combining stdout and stderr. A timeout or signal is reported as NOT passed.
func RunSuite(timeout time.Duration) SuiteRunner {
	return func(r Runner, root string) SuiteResult {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, r.Cmd, r.Args...)
		cmd.Dir = root
		cmd.Env = suiteEnv()
		// Without WaitDelay a killed test runner's surviving children hold the
		// output pipes open and CombinedOutput blocks long past the deadline
		// (cmd.exe's children on Windows, orphaned workers elsewhere).
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() == context.DeadlineExceeded
		// ErrWaitDelay means the process EXITED SUCCESSFULLY but an orphaned
		// child held the I/O pipes past WaitDelay — Go returns it INSTEAD of
		// nil in that case. The suite's own verdict is green; treating the
		// pipe-holder as RED manufactured phantom "tests failing" blocks.
		passed := (err == nil || errors.Is(err, exec.ErrWaitDelay)) && !timedOut
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		return SuiteResult{Passed: passed, Output: string(out), TimedOut: timedOut, Err: errText}
	}
}

// suiteEnv is the environment for the suite subprocess: a quiet, deterministic
// shell (CI=1, NO_COLOR=1) with every GIT_* variable scrubbed. The gate runs as
// a git pre-commit hook, so os.Environ() carries GIT_DIR / GIT_INDEX_FILE /
// GIT_WORK_TREE pointing at the repo being committed; leaking them into the
// suite's `go test` makes its git-e2e fixtures commit against the WRONG repo and
// clobber its HEAD. cleanGitEnv (in precommit.go, same package) drops them so the
// suite runs as if invoked from a plain shell.
func suiteEnv() []string {
	return append(cleanGitEnv(), "CI=1", "NO_COLOR=1")
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
