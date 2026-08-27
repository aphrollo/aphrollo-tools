package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRun returns a SuiteRunner that ignores its inputs and yields a fixed
// result, so PostEdit can be exercised without spawning a real test suite.
func fakeRun(passed bool, output string) SuiteRunner {
	return func(Runner, string) SuiteResult { return SuiteResult{Passed: passed, Output: output} }
}

// TestSuiteEnv_ScrubsGitVars guards the gate against corrupting the very repo
// it is committing. RunSuite spawns `go test`, which under the pre-commit hook
// would inherit GIT_DIR / GIT_INDEX_FILE / GIT_WORK_TREE pointing at the OUTER
// repo — the suite's git-e2e fixtures then commit against it and clobber HEAD.
// The suite must run as if invoked from a plain shell: no GIT_* leaks, while
// the quieting CI=1 / NO_COLOR=1 still get through.
func TestSuiteEnv_ScrubsGitVars(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	t.Setenv("GIT_INDEX_FILE", "/outer/.git/index")
	t.Setenv("GIT_WORK_TREE", "/outer")
	t.Setenv("KEEP_ME", "bar")

	env := suiteEnv()

	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") {
			t.Fatalf("suiteEnv leaked git var into the suite: %q", kv)
		}
	}
	var keptUser, hasCI, hasNoColor bool
	for _, kv := range env {
		switch kv {
		case "KEEP_ME=bar":
			keptUser = true
		case "CI=1":
			hasCI = true
		case "NO_COLOR=1":
			hasNoColor = true
		}
	}
	if !keptUser {
		t.Error("suiteEnv dropped a non-git var (KEEP_ME)")
	}
	if !hasCI || !hasNoColor {
		t.Errorf("suiteEnv must keep CI=1/NO_COLOR=1: CI=%v NO_COLOR=%v", hasCI, hasNoColor)
	}
}

// postPayload builds a PostToolUse payload for a Go project at root.
func postPayload(tool, file string) []byte {
	in := map[string]any{
		"session_id": "sess-post",
		"tool_name":  tool,
		"tool_input": map[string]any{"file_path": file},
	}
	b, _ := json.Marshal(in)
	return b
}

// TestPostEdit pins the loud-on-every-run contract (task A2, 2026-08-15):
// PostEdit used to be silent on green/writing-test/no-delta and only spoke up
// for RED. A session watching for the hook's advisory could not then tell
// "the suite ran and passed" from "the hook never fired at all" — both read
// as nothing. Every branch below must now say SOMETHING; "" is reserved for
// "there was nothing to test" (checked separately in
// TestPostEdit_SkipsNonActionable).
func TestPostEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	test := filepath.Join(root, "widget_test.go")

	cases := []struct {
		name        string
		payload     []byte
		passed      bool
		output      string
		wantContain string
	}{
		{"green source reports green", postPayload("Edit", src), true, "ok\nPASS", "→ green"},
		{"passing test edit reports green", postPayload("Write", test), true, "ok\nPASS", "→ green"},
		{"green with a parsed passed count", postPayload("Edit", src), true, "test result: ok. 7 passed; 0 failed", "→ green (7 passed,"},
		{"failing source reports red", postPayload("Edit", src), false, "--- FAIL: TestThing\n want 1", "outcome=red"},
		{"missing impl is clean red", postPayload("Edit", src), false, "undefined: NewWidget", "red-missing-impl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PostEdit(c.payload, fakeRun(c.passed, c.output))
			if !strings.Contains(got, c.wantContain) {
				t.Fatalf("output missing %q:\n%s", c.wantContain, got)
			}
		})
	}
}

func TestPostEdit_SkipsNonActionable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")

	// A non-code file is ignored entirely.
	if got := PostEdit(postPayload("Edit", filepath.Join(root, "README.md")), fakeRun(false, "boom")); got != "" {
		t.Fatalf("ignore file should be silent, got: %s", got)
	}
	// A tool that itself failed has nothing to test.
	in := map[string]any{
		"session_id":    "s",
		"tool_name":     "Edit",
		"tool_input":    map[string]any{"file_path": filepath.Join(root, "x.go")},
		"tool_response": map[string]any{"success": false},
	}
	b, _ := json.Marshal(in)
	if got := PostEdit(b, fakeRun(false, "boom")); got != "" {
		t.Fatalf("failed tool response should be silent, got: %s", got)
	}
	// Malformed JSON fails silent.
	if got := PostEdit([]byte("{bad"), fakeRun(false, "boom")); got != "" {
		t.Fatalf("malformed input should be silent, got: %s", got)
	}
}

func TestPostEdit_StampsState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")

	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))
	s, _ := loadSession("sess-post")
	if got := s.ByProject[root].Outcome; got != string(Green) {
		t.Fatalf("state outcome = %q, want green", got)
	}
}

// --- PostToolUse timeout backoff --------------------------------------------

// countingTimeoutRun is a SuiteRunner that always times out and counts its own
// invocations, so a test can assert the suite was (or was not) actually
// invoked — not just that PostEdit returned "".
func countingTimeoutRun(invoked *int) SuiteRunner {
	return func(Runner, string) SuiteResult {
		*invoked++
		return SuiteResult{TimedOut: true}
	}
}

// TestPostEdit_TimeoutBackoff_SkipsAfterTwoConsecutiveTimeouts pins the
// backoff: two consecutive timed-out runs at the SAME head SHA earn a SKIP —
// the third edit must not invoke the suite at all, not just discard its
// result. UPDATED for task A2 (2026-08-15): PostEdit used to go silent on a
// timeout; it now reports a TIMEOUT line for the first two (so a timeout is
// never mistaken for "ran and passed"), and a distinct SKIPPED line once the
// backoff engages, so "inconclusive, ran but proved nothing" and "did not
// even run" never look the same. (postPayload always uses the fixed
// "sess-post" session id, so state persists across these PostEdit calls
// within the test.)
func TestPostEdit_TimeoutBackoff_SkipsAfterTwoConsecutiveTimeouts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	src := filepath.Join(root, "widget.go")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	var invoked int
	run := countingTimeoutRun(&invoked)

	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "TIMEOUT") {
		t.Fatalf("a timed-out run must report TIMEOUT, got %q", got)
	}
	if invoked != 1 {
		t.Fatalf("first edit must invoke the suite, invoked=%d", invoked)
	}

	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "TIMEOUT") {
		t.Fatalf("a timed-out run must report TIMEOUT, got %q", got)
	}
	if invoked != 2 {
		t.Fatalf("second edit must invoke the suite, invoked=%d", invoked)
	}

	got := PostEdit(postPayload("Edit", src), run)
	if !strings.Contains(got, "SKIPPED") {
		t.Fatalf("a backed-off run must report SKIPPED, got %q", got)
	}
	if strings.Contains(got, "TIMEOUT") {
		t.Fatalf("a SKIPPED run never even invoked the suite — it must not also claim TIMEOUT, got %q", got)
	}
	if invoked != 2 {
		t.Fatalf("third edit must SKIP the suite entirely (streak >= 2 at the same head), invoked=%d", invoked)
	}
}

// TestPostEdit_TimeoutBackoff_CompletedRunResetsStreak pins that a run which
// actually COMPLETES (green or red, just not a timeout) resets the streak, so
// a later timeout gets a fresh two-attempt budget rather than staying wedged
// at the skip threshold forever. The first timeout is kept BELOW the skip
// threshold (streak 1) deliberately: once streak reaches 2, PostEdit skips
// the suite BEFORE invoking it at all (by design — it doesn't know a fake
// runner would have completed), so a completed run can only ever land while
// still under budget. That is exactly the production case this test pins:
// occasional/flaky timeouts alternating with completions never accumulate.
func TestPostEdit_TimeoutBackoff_CompletedRunResetsStreak(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	src := filepath.Join(root, "widget.go")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	var invoked int
	run := countingTimeoutRun(&invoked)

	PostEdit(postPayload("Edit", src), run) // streak 1 — still under the skip threshold

	// A completed run (not a timeout) must reset the streak to 0.
	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	s, _ := loadSession("sess-post")
	if ps := s.ByProject[root]; ps.TimeoutStreak != 0 || ps.TimeoutSHA != "" {
		t.Fatalf("a completed run must reset the timeout streak, got streak=%d sha=%q", ps.TimeoutStreak, ps.TimeoutSHA)
	}

	// The budget must be genuinely fresh: two MORE consecutive timeouts are
	// needed again before a third one skips — not just one.
	invoked = 0
	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "TIMEOUT") || invoked != 1 {
		t.Fatalf("post-reset first timeout must invoke the suite and report TIMEOUT, invoked=%d got=%q", invoked, got)
	}
	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "TIMEOUT") || invoked != 2 {
		t.Fatalf("post-reset second timeout must invoke the suite and report TIMEOUT, invoked=%d got=%q", invoked, got)
	}
	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "SKIPPED") || invoked != 2 {
		t.Fatalf("post-reset third timeout must skip the suite entirely and report SKIPPED, invoked=%d got=%q", invoked, got)
	}
}

// TestPostEdit_TimeoutBackoff_NewHeadSHARetries pins the re-arm: once the
// skip threshold is hit, landing a commit (moving HEAD) must grant a fresh
// attempt even though nothing else about the streak was reset explicitly.
func TestPostEdit_TimeoutBackoff_NewHeadSHARetries(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	src := filepath.Join(root, "widget.go")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	var invoked int
	run := countingTimeoutRun(&invoked)

	PostEdit(postPayload("Edit", src), run) // streak 1 @ SHA A
	PostEdit(postPayload("Edit", src), run) // streak 2 @ SHA A
	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "SKIPPED") || invoked != 2 {
		t.Fatalf("expected a skip before the commit, invoked=%d got=%q", invoked, got)
	}

	// A commit lands — HEAD moves to a new SHA.
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	if got := PostEdit(postPayload("Edit", src), run); !strings.Contains(got, "TIMEOUT") {
		t.Fatalf("a timed-out run must report TIMEOUT, got %q", got)
	}
	if invoked != 3 {
		t.Fatalf("a new head SHA must get a fresh attempt, invoked=%d", invoked)
	}
}

func TestRenderPostToolUse(t *testing.T) {
	if b, code := RenderPostToolUse(""); b != nil || code != 0 {
		t.Fatalf("empty text should be silent, got (%q,%d)", b, code)
	}
	b, code := RenderPostToolUse("hello")
	if code != 0 {
		t.Fatalf("post never blocks, exit = %d", code)
	}
	var out postToolUseOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.HookSpecificOutput.AdditionalContext != "hello" {
		t.Fatalf("unexpected envelope: %+v", out)
	}
}
