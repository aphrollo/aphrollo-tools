package cli

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// offHookRun feeds one hook payload to `aphrollo gate <hook>` and answers its
// stdout and exit code.
func offHookRun(t *testing.T, hook, payload string) (string, int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := runGate([]string{hook}, strings.NewReader(payload), &out, &errBuf)
	return out.String(), code
}

func offPayload(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A hook payload for each session hook, the kind that makes the hook speak when
// the session is on.
func offPayloadFor(t *testing.T, hook, session, cwd string) string {
	f := map[string]any{"session_id": session, "cwd": cwd, "hook_event_name": hook}
	switch hook {
	case "pretooluse":
		f["tool_name"], f["tool_input"] = "Bash", map[string]any{"command": "git reset --hard HEAD~1"}
	case "posttooluse", "posttoolusefailure":
		f["tool_name"], f["tool_input"] = "Bash", map[string]any{"command": "echo hi"}
	case "userpromptsubmit":
		f["prompt"] = "keep working"
	}
	return offPayload(t, f)
}

// Every session hook of an off session is silent and decides nothing: no
// context, no deny, no block, exit 0. The table is the dispatcher's own list of
// hooks (sessionHooks), so a hook added without a row fails the dispatch, not
// this test.
func TestSessionHooks_AnOffSessionMakesEveryHookSilent(t *testing.T) {
	gateConfigDir(t)
	cwd := t.TempDir()
	const session = "sess-off-table"
	switchOff := func(t *testing.T) {
		t.Helper()
		if out, _ := offHookRun(t, "userpromptsubmit", offPayload(t, map[string]any{"session_id": session, "cwd": cwd, "prompt": "/aphrollo off"})); !strings.Contains(out, "OFF") {
			t.Fatalf("/aphrollo off did not answer OFF: %s", out)
		}
	}

	hooks := make([]string, 0, len(sessionHooks))
	for hook := range sessionHooks {
		hooks = append(hooks, hook)
	}
	slices.Sort(hooks)
	for _, hook := range hooks {
		t.Run(hook, func(t *testing.T) {
			switchOff(t) // the session end of an earlier row dropped the session's file
			out, code := offHookRun(t, hook, offPayloadFor(t, hook, session, cwd))
			if out != "" || code != 0 {
				t.Errorf("an off session's %s must be silent and decide nothing, got exit %d and %q", hook, code, out)
			}
		})
	}
}

// The control: the same payloads speak while the session is on, so the silence
// above is the switch's and not an input that never made a hook speak.
func TestSessionHooks_TheSamePayloadsSpeakWhileTheSessionIsOn(t *testing.T) {
	gateConfigDir(t)
	cwd := t.TempDir()
	for _, hook := range []string{"pretooluse", "userpromptsubmit", "sessionstart"} {
		out, code := offHookRun(t, hook, offPayloadFor(t, hook, "sess-on-control", cwd))
		if out == "" && code == 0 {
			t.Errorf("%s said nothing for an on session: the off test proves nothing for it", hook)
		}
	}
}

// What stays on: a wall that blocks every author still blocks while the
// session is off. The live hook has no secrets detector yet, so the test puts
// one in the always-on list and checks the off path consults it.
func TestSessionHooks_AnAlwaysOnWallStillBlocksWhileTheSessionIsOff(t *testing.T) {
	gateConfigDir(t)
	cwd := t.TempDir()
	const session = "sess-off-wall"
	offHookRun(t, "userpromptsubmit", offPayload(t, map[string]any{"session_id": session, "cwd": cwd, "prompt": "/aphrollo off"}))
	saved := alwaysOnWalls
	t.Cleanup(func() { alwaysOnWalls = saved })
	alwaysOnWalls = []func([]byte) tdd.Decision{
		func([]byte) tdd.Decision {
			return tdd.Decision{Action: tdd.Block, Reason: "secrets: a secret in the write", Policy: "secrets"}
		},
	}

	out, code := offHookRun(t, "pretooluse", offPayloadFor(t, "pretooluse", session, cwd))

	if !strings.Contains(out, "secrets") || code == 0 && !strings.Contains(out, "deny") {
		t.Errorf("the always-on wall must still block an off session's PreToolUse, got exit %d and %q", code, out)
	}
}

// An unknown hook is still refused, and TRELLIS_OFF switches the whole process.
func TestSessionHooks_TheEnvironmentSwitchesEveryHookOfTheProcess(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("TRELLIS_OFF", "1")

	for hook := range sessionHooks {
		if hook == "userpromptsubmit" || hook == "sessionend" {
			continue
		}
		if out, code := offHookRun(t, hook, offPayloadFor(t, hook, "sess-env", t.TempDir())); out != "" || code != 0 {
			t.Errorf("TRELLIS_OFF must silence %s, got exit %d and %q", hook, code, out)
		}
	}
	if _, code := offHookRun(t, "frobnicate", "{}"); code != 2 {
		t.Errorf("an unknown hook must still be refused, exit %d", code)
	}
}
