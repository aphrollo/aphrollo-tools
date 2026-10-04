package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// hookRepo is a directory git would call a repository, with the gate state and
// event log of the test.
func hookRepo(t *testing.T) string {
	t.Helper()
	gateConfigDir(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/lane/t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func runHook(t *testing.T, hook string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	Run([]string{"gate", hook}, bytes.NewReader(raw), &bytes.Buffer{}, &bytes.Buffer{})
}

// Every Claude hook run leaves a hook.timing event: which hook, how long, and
// who called it. The userpromptsubmit ones are the message boundaries.
func TestGateHook_EachRunWritesAHookTimingEventForItsActor(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo := hookRepo(t)

	runHook(t, "userpromptsubmit", map[string]any{"session_id": "s1", "cwd": repo, "prompt": "hello"})
	runHook(t, "sessionend", map[string]any{"session_id": "s1", "agent_id": "a2", "cwd": repo})

	got := eventsOfKind(tdd.ReadEvents(repo), "hook.timing")
	if len(got) != 2 {
		t.Fatalf("%d hook.timing events, want 2: %+v", len(got), got)
	}
	if got[0].Detail["hook"] != "userpromptsubmit" || got[0].Actor != "s1" || got[0].Lane != "lane/t" || got[0].Secs < 0 {
		t.Errorf("first event = %+v, want userpromptsubmit by s1 on lane/t", got[0])
	}
	if got[1].Detail["hook"] != "sessionend" || got[1].Actor != "s1/a2" {
		t.Errorf("second event = %+v, want sessionend by s1/a2 (a subagent names its agent)", got[1])
	}
}

func TestGateHook_APayloadThatIsNotJSONStillGetsItsTiming(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateConfigDir(t)

	Run([]string{"gate", "sessionend"}, strings.NewReader("{broken"), &bytes.Buffer{}, &bytes.Buffer{})

	got := eventsOfKind(tdd.ReadEvents(""), "hook.timing")
	if len(got) != 1 || got[0].Detail["hook"] != "sessionend" {
		t.Fatalf("hook.timing events = %+v, want one for sessionend", got)
	}
}
