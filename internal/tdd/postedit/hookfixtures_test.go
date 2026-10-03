package postedit

import (
	"encoding/json"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// Every payload the gate's hooks read is decoded here from a recording of the
// real harness, through the same struct the hook decodes it with. A field the
// gate reads that the recording does not carry shows as an empty value, and
// the test names it.

// eachRecorded runs fn over the recordings of one event, narrowed to one tool
// when tool is not empty.
func eachRecorded(t *testing.T, event, tool string, fn func(t *testing.T, c tddtest.HookCase, raw []byte)) {
	t.Helper()
	n := 0
	for _, c := range tddtest.HookCases {
		if c.Event != event || (tool != "" && c.Tool != tool) {
			continue
		}
		n++
		t.Run(c.File, func(t *testing.T) { fn(t, c, tddtest.HookFixture(t, c.File)) })
	}
	if n == 0 {
		t.Fatalf("no recording of %s %s", event, tool)
	}
}

func decode(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("a recorded payload does not decode: %v", err)
	}
}

func TestRecordedStopPayloads_DecodeThroughStopInput(t *testing.T) {
	for _, event := range []string{"Stop", "SubagentStop"} {
		t.Run(event, func(t *testing.T) {
			eachRecorded(t, event, "", func(t *testing.T, c tddtest.HookCase, raw []byte) {
				var in stopInput
				decode(t, raw, &in)

				if in.SessionID != tddtest.HookSession || in.Cwd != c.Cwd || in.StopHookActive {
					t.Fatalf("stopInput = %+v, want session %q, cwd %q, stop_hook_active false", in, tddtest.HookSession, c.Cwd)
				}
			})
		})
	}
}

func TestRecordedStopPayloads_ASubagentStopNamesTheSubagentsWorktreeNotTheSessionsDir(t *testing.T) {
	var sub, main stopInput
	decode(t, tddtest.HookFixture(t, "subagentstop.json"), &sub)
	decode(t, tddtest.HookFixture(t, "stop.json"), &main)

	if sub.Cwd != tddtest.HookAgentCwd || main.Cwd != tddtest.HookCwd {
		t.Fatalf("cwd: SubagentStop %q, Stop %q, want the worktree %q and the session dir %q", sub.Cwd, main.Cwd, tddtest.HookAgentCwd, tddtest.HookCwd)
	}
}

func TestRecordedPostToolUsePayloads_DecodeThroughPostToolUseInput(t *testing.T) {
	eachRecorded(t, "PostToolUse", "", func(t *testing.T, c tddtest.HookCase, raw []byte) {
		var in postToolUseInput
		decode(t, raw, &in)

		if in.SessionID != tddtest.HookSession || in.ToolName != c.Tool || in.ToolInput.FilePath != c.FilePath {
			t.Fatalf("postToolUseInput = %+v, want session %q, tool %q, file %q", in, tddtest.HookSession, c.Tool, c.FilePath)
		}
		// The harness's Write response is {type, filePath, ...}: it carries no
		// success flag, so the "tool itself failed" branch cannot be reached
		// from a PostToolUse payload (a failed call raises PostToolUseFailure).
		if in.ToolResponse.Success != nil {
			t.Fatalf("tool_response.success = %v, but the recorded response carries none", *in.ToolResponse.Success)
		}
	})
}

func TestRecordedBashPayload_DecodesThroughTheBashReaders(t *testing.T) {
	eachRecorded(t, "PreToolUse", "Bash", func(t *testing.T, c tddtest.HookCase, raw []byte) {
		var snap bashInput
		var suite bashSuiteInput
		decode(t, raw, &snap)
		decode(t, raw, &suite)

		if snap.SessionID != tddtest.HookSession || snap.ToolUseID == "" || snap.Cwd != c.Cwd || snap.ToolName != "Bash" || snap.ToolInput.Command != c.Command {
			t.Fatalf("bashInput = %+v, want session %q, a tool_use_id, cwd %q, Bash, command %q", snap, tddtest.HookSession, c.Cwd, c.Command)
		}
		if suite.ToolName != "Bash" || suite.Cwd != c.Cwd || suite.ToolInput.Command != c.Command {
			t.Fatalf("bashSuiteInput = %+v, want Bash, cwd %q, command %q", suite, c.Cwd, c.Command)
		}
		if !IsBashHook(raw) {
			t.Fatal("IsBashHook = false for a recorded Bash call")
		}
	})
}

func TestRecordedWritePayload_IsNotABashHook(t *testing.T) {
	if IsBashHook(tddtest.HookFixture(t, "pretooluse_write.json")) {
		t.Fatal("IsBashHook = true for a recorded Write call")
	}
}

func TestRecordedPreToolUsePayloads_DecodeThroughTheEditGuards(t *testing.T) {
	eachRecorded(t, "PreToolUse", "", func(t *testing.T, c tddtest.HookCase, raw []byte) {
		var primary primaryGateInput
		var advisory worktreeAdvisoryInput
		decode(t, raw, &primary)
		decode(t, raw, &advisory)

		if primary.SessionID != tddtest.HookSession || primary.ToolName != c.Tool || primary.Cwd != c.Cwd ||
			primary.ToolInput.FilePath != c.FilePath || primary.ToolInput.Command != c.Command {
			t.Fatalf("primaryGateInput = %+v, want session %q, tool %q, cwd %q, file %q, command %q",
				primary, tddtest.HookSession, c.Tool, c.Cwd, c.FilePath, c.Command)
		}
		if advisory.SessionID != tddtest.HookSession || advisory.ToolName != c.Tool || advisory.ToolInput.FilePath != c.FilePath {
			t.Fatalf("worktreeAdvisoryInput = %+v, want session %q, tool %q, file %q", advisory, tddtest.HookSession, c.Tool, c.FilePath)
		}
	})
}
