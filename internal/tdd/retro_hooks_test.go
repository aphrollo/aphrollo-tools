package tdd

import (
	"strings"
	"testing"
)

// The retro a merge records reaches the session through its hooks, once.
// These tests record one directly and read it back through each hook.

const hookRetro = "retro #839 lane/probe-discard (43m open→merge):\n#839: 1 push after open\n"

func TestHandlePrompt_DeliversTheSessionsPendingRetroOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if err := recordRetro("sess-prompt", "", 839, hookRetro); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"prompt":"what next?","session_id":"sess-prompt","cwd":"."}`)

	if got := HandlePrompt(raw).Message; !strings.Contains(got, "#839: 1 push after open") {
		t.Fatalf("the prompt hook did not print the pending retro: %q", got)
	}
	if got := HandlePrompt(raw).Message; strings.Contains(got, "#839") {
		t.Errorf("the prompt hook printed the retro a second time: %q", got)
	}
}

func TestWithPendingRetro_AppendsTheRetroAfterTheGateLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if err := recordRetro("sess-post", "", 839, hookRetro); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"session_id":"sess-post","tool_name":"Bash"}`)

	got := WithPendingRetro(raw, "gate: go test ./x → green")
	if want := "gate: go test ./x → green\n\n" + strings.TrimRight(hookRetro, "\n"); got != want {
		t.Fatalf("PostToolUse text = %q, want %q", got, want)
	}
	if got := WithPendingRetro(raw, "gate: go test ./x → green"); got != "gate: go test ./x → green" {
		t.Errorf("the retro was appended a second time: %q", got)
	}
	if got := WithPendingRetro(raw, ""); got != "" {
		t.Errorf("a hook with nothing to say printed %q", got)
	}
}

func TestHandleSessionStart_SurfacesASessionlessRetroInItsRepoOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	if err := recordRetro("", repo, 839, hookRetro); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"session_id":"sess-start","cwd":` + jsonString(repo) + `}`)

	if got := HandleSessionStart(raw); !strings.Contains(got, "#839: 1 push after open") {
		t.Fatalf("session start in the merging repo did not print the retro:\n%s", got)
	}
	if got := HandleSessionStart(raw); strings.Contains(got, "#839") {
		t.Errorf("session start printed the retro a second time:\n%s", got)
	}
}
