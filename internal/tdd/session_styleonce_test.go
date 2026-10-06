package tdd

import (
	"strings"
	"testing"
	"time"
)

func styleonceCarriesStyle(t *testing.T, r PromptResult) bool {
	t.Helper()
	return strings.Contains(r.Style, "Reply style: terse")
}

// The reply-style block cost about 90 tokens on every prompt of a session. It
// is sent once, with the session's first prompt, and not again.
func TestHandlePrompt_TheStyleBlockIsSentOncePerSessionNotOnEveryPrompt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-style-once"

	first := HandlePrompt(promptJSON("keep working", sess, ""))
	second := HandlePrompt(promptJSON("and again", sess, ""))

	if !styleonceCarriesStyle(t, first) {
		t.Errorf("the first prompt must carry the style block, got %+v", first)
	}
	if second.Style != "" {
		t.Errorf("a later prompt must not repeat the style block, got %q", second.Style)
	}
}

// A new SessionStart (a resume, or a compaction that dropped the context) has
// the block sent again with the next prompt.
func TestHandlePrompt_ASessionStartHasTheNextPromptCarryTheStyleAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-style-restart"
	HandlePrompt(promptJSON("one", sess, ""))

	HandleSessionStart([]byte(`{"session_id":"` + sess + `"}`))

	if !styleonceCarriesStyle(t, HandlePrompt(promptJSON("two", sess, ""))) {
		t.Error("the prompt after a session start must carry the style block again")
	}
}

// `/tdd style terse` after plain turns the block back on: it goes out with that
// command's own answer, and once.
func TestHandlePrompt_TurningTerseBackOnSendsTheStyleAgainOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-style-reenable"
	HandlePrompt(promptJSON("one", sess, ""))
	HandlePrompt(promptJSON("/tdd style plain", sess, ""))

	if !styleonceCarriesStyle(t, HandlePrompt(promptJSON("/tdd style terse", sess, ""))) {
		t.Error("the answer to /tdd style terse must carry the style block")
	}
	if next := HandlePrompt(promptJSON("two", sess, "")); next.Style != "" {
		t.Errorf("the prompt after it must not repeat the block, got %q", next.Style)
	}
}

// A hook with no session id has nowhere to remember that it sent the block, so
// it keeps sending it rather than never.
func TestHandlePrompt_APromptWithNoSessionKeepsCarryingTheStyle(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	for range 2 {
		if !styleonceCarriesStyle(t, HandlePrompt(promptJSON("go", "", ""))) {
			t.Fatal("a prompt with no session must carry the style block")
		}
	}
}

// SessionStart is one line: the nudge, with no style block and no wrapped
// paragraphs behind it.
func TestHandleSessionStart_IsOneLineWithNoStyleBlock(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	maybeWeeklyDigest(time.Now(), false) // the weekly digest is its own once-a-week line

	msg := HandleSessionStart([]byte(`{"session_id":"ss-oneline"}`))

	if strings.Contains(msg, "\n") || strings.Contains(msg, "Reply style") {
		t.Errorf("session-start context must be one line without the style block:\n%s", msg)
	}
	if !strings.Contains(msg, "`tdd` skill") {
		t.Errorf("the one line must still send the session to the tdd skill:\n%s", msg)
	}
}
