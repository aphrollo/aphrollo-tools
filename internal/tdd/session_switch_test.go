package tdd

import (
	"strings"
	"testing"
)

// `/aphrollo off|on|status` is the session's switch; `/tdd ...` and `/gate ...`
// stay as aliases of the same one.
func TestHandlePrompt_AphrolloOffAndOnAreTheSameSwitchAsTdd(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	const sess = "sess-switch-1"

	off := HandlePrompt(promptJSON("/aphrollo off", sess, ""))
	if !off.Block || !strings.Contains(off.Message, "OFF") {
		t.Fatalf("/aphrollo off: got %+v", off)
	}
	if !SessionOff(sess) {
		t.Fatal("/aphrollo off did not switch the session off")
	}
	if st := HandlePrompt(promptJSON("/tdd status", sess, "")); !strings.Contains(st.Message, "OFF") {
		t.Fatalf("/tdd status must read the same switch, got %q", st.Message)
	}

	on := HandlePrompt(promptJSON("/aphrollo on", sess, ""))
	if !on.Block || !strings.Contains(on.Message, "ON") || SessionOff(sess) {
		t.Fatalf("/aphrollo on: got %+v, off=%v", on, SessionOff(sess))
	}
}

// One line: on or off, and what stays on whatever the switch says.
func TestHandlePrompt_AphrolloStatusIsOneLineNamingWhatStaysOn(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	const sess = "sess-switch-2"

	for _, state := range []string{"ON", "OFF"} {
		if state == "OFF" {
			HandlePrompt(promptJSON("/aphrollo off", sess, ""))
		}
		msg := HandlePrompt(promptJSON("/aphrollo status", sess, "")).Message
		if strings.Contains(msg, "\n") || !strings.Contains(msg, state) {
			t.Errorf("status with the switch %s is not one line naming it: %q", state, msg)
		}
		for _, stays := range []string{"git-side gates"} {
			if !strings.Contains(msg, stays) {
				t.Errorf("status must say %q stays on: %q", stays, msg)
			}
		}
	}
}

// Each flip is an event with its session and repo, so stats count it as the
// wrong-block signal it is.
func TestHandlePrompt_EachSwitchIsAnEventWithItsSessionAndRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo := t.TempDir()
	const sess = "sess-switch-3"

	HandlePrompt(promptJSON("/aphrollo off", sess, repo))
	HandlePrompt(promptJSON("/aphrollo on", sess, repo))

	got := map[string]string{}
	for _, e := range ReadEvents(repo) {
		if e.Kind == "override" {
			got[e.Verdict] = e.Detail["switch"] + " " + e.Detail["file"]
		}
	}
	for verdict, want := range map[string]string{"override-off": "session-off " + sess, "override-on": "session-on " + sess} {
		if got[verdict] != want {
			t.Errorf("event %s carries %q, want %q (all: %v)", verdict, got[verdict], want, got)
		}
	}
}

// While the session is off a prompt carries nothing: no style block, no red
// reminder, no harvest. The commands still answer, or the switch could not be
// turned back on.
func TestHandlePrompt_AnOffSessionAddsNothingToAPromptButStillAnswersTheSwitch(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	const sess = "sess-switch-4"
	HandlePrompt(promptJSON("/aphrollo off", sess, ""))

	if r := HandlePrompt(promptJSON("keep working", sess, "")); r.Message != "" || r.Style != "" || r.Block {
		t.Errorf("an off session's prompt must carry nothing, got %+v", r)
	}
	if r := HandlePrompt(promptJSON("/aphrollo status", sess, "")); !r.Block || !strings.Contains(r.Message, "OFF") {
		t.Errorf("the switch must still answer while off, got %+v", r)
	}
}

// TRELLIS_OFF is the same switch for a whole process, whatever the session.
func TestSessionOff_TheEnvironmentSwitchesTheWholeProcess(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if SessionOff("sess-switch-5") || SessionOff("") {
		t.Fatal("a session nobody switched off is on")
	}
	t.Setenv("TRELLIS_OFF", "1")
	if !SessionOff("sess-switch-5") || !SessionOff("") {
		t.Fatal("TRELLIS_OFF=1 must switch every session of the process off")
	}
}

func TestStatusLine_ShowsOffForTheEnvironmentSwitchToo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_OFF", "1")

	if got := StatusLine([]byte(`{"session_id":"sess-switch-6","cwd":""}`)); !strings.Contains(got, "off") {
		t.Errorf("the badge must say off under TRELLIS_OFF, got %q", got)
	}
}
