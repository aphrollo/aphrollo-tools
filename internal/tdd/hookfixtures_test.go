package tdd

import (
	"encoding/json"
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// The session-lifecycle hooks decode recordings of the real harness through
// the structs the hooks use. A field the gate reads that the recording does not
// carry shows as an empty value, and the test names it.

func recordedCase(t *testing.T, file string) (tddtest.HookCase, []byte) {
	t.Helper()
	for _, c := range tddtest.HookCases {
		if c.File == file {
			return c, tddtest.HookFixture(t, file)
		}
	}
	t.Fatalf("no recording named %s", file)
	return tddtest.HookCase{}, nil
}

func TestRecordedUserPromptSubmit_DecodesThroughPromptInput(t *testing.T) {
	c, raw := recordedCase(t, "userpromptsubmit.json")
	var in promptInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}

	if in.SessionID != tddtest.HookSession || in.Cwd != c.Cwd || !strings.HasPrefix(in.Prompt, c.PromptPrefix) {
		t.Fatalf("promptInput = %+v, want session %q, cwd %q, a prompt starting %q", in, tddtest.HookSession, c.Cwd, c.PromptPrefix)
	}
}

func TestRecordedSessionStart_DecodesThroughSessionStartInput(t *testing.T) {
	c, raw := recordedCase(t, "sessionstart.json")
	var in sessionStartInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}

	if in.SessionID != tddtest.HookSession || in.Cwd != c.Cwd {
		t.Fatalf("sessionStartInput = %+v, want session %q, cwd %q", in, tddtest.HookSession, c.Cwd)
	}
}

func TestRecordedSessionEnd_DecodesThroughSessionEndInput(t *testing.T) {
	_, raw := recordedCase(t, "sessionend.json")
	var in sessionEndInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}

	if in.SessionID != tddtest.HookSession {
		t.Fatalf("sessionEndInput = %+v, want session %q", in, tddtest.HookSession)
	}
}
