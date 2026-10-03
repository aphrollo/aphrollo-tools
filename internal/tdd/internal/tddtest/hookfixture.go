package tddtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The payloads under testdata/hooks are what the agent harness really sent a
// hook, recorded once (see the README beside them). HookFixture and HookPayload
// hand them to a test; HookCases says what each one holds, written out by hand
// so a decoder is judged against the recording and not against itself.

const (
	// HookSession and HookPrompt are the scrubbed ids every recording carries.
	HookSession = "00000000-0000-4000-8000-000000000001"
	HookPrompt  = "00000000-0000-4000-8000-000000000002"
	// HookCwd is the session's working directory in a recording; HookAgentCwd
	// is the worktree a subagent runs in. Both are Windows paths, as recorded.
	HookCwd      = `C:\Users\dev\spike-hooks`
	HookAgentCwd = `C:\Users\dev\spike-hooks\.claude\worktrees\agent-a26c10f2ce9a15725`
	// HookAgentID is the id of the subagent the recording started.
	HookAgentID = "a26c10f2ce9a15725"
	// HookAgentType is the subagent type the recording started.
	HookAgentType = "general-purpose"
)

// HookCase is what one recorded payload holds. A field left empty is a field
// the payload does not carry.
type HookCase struct {
	File  string
	Event string
	// Cwd is the payload's cwd; every recording carries one.
	Cwd string
	// Tool, FilePath, Command and Content are the tool name and the
	// tool_input fields of a PreToolUse, PostToolUse or PostToolUseFailure.
	Tool, FilePath, Command, Content string
	// AgentID and AgentType are carried only by a payload raised inside a
	// subagent (SubagentStart and SubagentStop name theirs too).
	AgentID, AgentType string
	// PromptPrefix is the start of a UserPromptSubmit prompt.
	PromptPrefix string
	// StopHookActive says the payload carries a stop_hook_active key (always
	// false in a recording).
	StopHookActive bool
	// BatchTools are the tool_calls[].tool_name of a PostToolBatch, in order.
	BatchTools []string
}

// HookCases lists every recorded payload.
var HookCases = []HookCase{
	{File: "sessionstart.json", Event: "SessionStart", Cwd: HookCwd},
	{File: "userpromptsubmit.json", Event: "UserPromptSubmit", Cwd: HookCwd,
		PromptPrefix: "This is a throwaway test repo; do exactly these steps"},
	{File: "pretooluse_write.json", Event: "PreToolUse", Cwd: HookCwd,
		Tool: "Write", FilePath: HookCwd + `\a.txt`, Content: "a"},
	{File: "pretooluse_bash.json", Event: "PreToolUse", Cwd: HookCwd,
		Tool: "Bash", Command: "ls"},
	{File: "pretooluse_subagent_write.json", Event: "PreToolUse", Cwd: HookAgentCwd,
		Tool: "Write", FilePath: HookAgentCwd + `\b.txt`, Content: "b\n",
		AgentID: HookAgentID, AgentType: HookAgentType},
	{File: "posttooluse_write.json", Event: "PostToolUse", Cwd: HookCwd,
		Tool: "Write", FilePath: HookCwd + `\a.txt`, Content: "a"},
	{File: "posttooluse_subagent_write.json", Event: "PostToolUse", Cwd: HookAgentCwd,
		Tool: "Write", FilePath: HookAgentCwd + `\b.txt`, Content: "b\n",
		AgentID: HookAgentID, AgentType: HookAgentType},
	{File: "posttoolusefailure_bash.json", Event: "PostToolUseFailure", Cwd: HookCwd,
		Tool: "Bash", Command: "false"},
	{File: "posttoolbatch.json", Event: "PostToolBatch", Cwd: HookCwd,
		BatchTools: []string{"Write", "ToolSearch"}},
	{File: "posttoolbatch_subagent.json", Event: "PostToolBatch", Cwd: HookAgentCwd,
		AgentID: HookAgentID, AgentType: HookAgentType, BatchTools: []string{"Write"}},
	{File: "subagentstart.json", Event: "SubagentStart", Cwd: HookAgentCwd,
		AgentID: HookAgentID, AgentType: HookAgentType},
	{File: "subagentstop.json", Event: "SubagentStop", Cwd: HookAgentCwd,
		AgentID: HookAgentID, AgentType: HookAgentType, StopHookActive: true},
	{File: "stop.json", Event: "Stop", Cwd: HookCwd, StopHookActive: true},
	{File: "sessionend.json", Event: "SessionEnd", Cwd: HookCwd},
}

// hookFixtureDir finds testdata/hooks by walking up from the test's working
// directory, so a test in any package of the tree reaches it the same way.
func hookFixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "internal", "tdd", "internal", "tddtest", "testdata", "hooks")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("testdata/hooks not found above the test's working directory")
		}
		dir = parent
	}
}

// HookFixture reads one recorded payload by its file name under testdata/hooks
// (a hand-written one lives under handwritten/).
func HookFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hookFixtureDir(t), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// HookPayload is a recorded payload with the named top-level fields replaced,
// the rest left as the harness sent it.
func HookPayload(t *testing.T, name string, overlay map[string]any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(HookFixture(t, name), &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range overlay {
		m[k] = v
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
