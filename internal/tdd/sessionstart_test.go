package tdd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHandleSessionStart_NudgesSkills pins the CONTRACT the nudge must state,
// not its exact wording: it names a skill that EXISTS on the box — the `tdd`
// skill `gate init` writes, not a plugin that may be uninstalled — and it says
// the hooks, not the model, run the tests, so a session stops re-running
// suites the loud gates already ran and reported. Checking behaviorally
// important phrases rather than the full string keeps this from being a change
// detector on prose.
func TestHandleSessionStart_NudgesSkills(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	msg := HandleSessionStart([]byte(`{"session_id":"ss-1"}`))
	for _, want := range []string{"`tdd` skill", "hooks run the tests", "TIMEOUT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("session-start nudge missing %q:\n%s", want, msg)
		}
	}
	// Both are skills this box no longer installs: a nudge naming one sends
	// the session looking for a file that is not there.
	for _, dead := range []string{"superpowers:", "incremental-implementation"} {
		if strings.Contains(msg, dead) {
			t.Errorf("nudge references the uninstalled %q:\n%s", dead, msg)
		}
	}
}

// TestHandleSessionStart_NudgeNamesTheInstalledSkillPath pins the fix for the
// nudge that named the skill but never its location: an agent with no Skill
// tool (Claude Code's `builder`/`researcher` types) cannot invoke a skill by
// name and has nothing else to open. Once the skill is actually written to
// this config dir, the nudge must state that exact resolved path so "read its
// SKILL.md" is actionable without a filesystem search.
func TestHandleSessionStart_NudgeNamesTheInstalledSkillPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if _, err := WriteTDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	msg := HandleSessionStart([]byte(`{"session_id":"ss-skillpath"}`))
	want := skillPath(dir)
	if !strings.Contains(msg, want) {
		t.Errorf("nudge does not state the installed skill's resolved path %q:\n%s", want, msg)
	}
}

// TestHandleSessionStart_NudgeOmitsPathWhenSkillMissing pins the other half:
// a nudge pointing at a file that does not exist is worse than one pointing
// at nothing, so when the skill was never installed the nudge must not print
// any path for it — it must instead name the command that installs it.
func TestHandleSessionStart_NudgeOmitsPathWhenSkillMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	msg := HandleSessionStart([]byte(`{"session_id":"ss-noskill"}`))
	if strings.Contains(msg, skillPath(dir)) {
		t.Errorf("nudge names a skill path that does not exist on disk:\n%s", msg)
	}
	if !strings.Contains(msg, "aphrollo install") {
		t.Errorf("nudge must name the install command when the skill is missing:\n%s", msg)
	}
}

func TestHandleSessionStart_SilentWhenOff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "ss-off"
	if err := setOff(sess, true); err != nil {
		t.Fatal(err)
	}
	if msg := HandleSessionStart([]byte(`{"session_id":"` + sess + `"}`)); msg != "" {
		t.Fatalf("nudge should be silent when TDD enforcement is off, got %q", msg)
	}
}

func TestRenderSessionStart(t *testing.T) {
	body, code := RenderSessionStart("hello")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q, want SessionStart", out.HookSpecificOutput.HookEventName)
	}
	if out.HookSpecificOutput.AdditionalContext != "hello" {
		t.Fatalf("additionalContext = %q, want hello", out.HookSpecificOutput.AdditionalContext)
	}
	if b, c := RenderSessionStart(""); b != nil || c != 0 {
		t.Fatalf("empty nudge should be silent, got body=%q code=%d", b, c)
	}
}
