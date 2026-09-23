package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The discard-wall directive is wired into `aphrollo gate pretooluse`
// itself now, replacing the ad hoc `grep -P` hook that used to scan the raw
// command TEXT and could not tell a real `git checkout -- f` from the same
// words sitting inside a quoted argument (issue #725's class of bug, for
// the discard wall rather than the redirect scan).
func discardBashPayload(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":   "Bash",
		"session_id":  "cli-discard-bash",
		"tool_use_id": "toolu_1",
		"cwd":         t.TempDir(),
		"tool_input":  map[string]any{"command": cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRun_PreToolUse_DeniesARealDiscardingGitInvocation(t *testing.T) {
	cfg := gateConfigDir(t)

	var out, errb bytes.Buffer
	stdin := strings.NewReader(discardBashPayload(t, "git checkout -- f"))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied)\nstdout:%s", code, out.String())
	}
	reason := denyReason(t, out.Bytes())
	if !strings.Contains(reason, "forbidden: they discard uncommitted work") {
		t.Fatalf("the deny must name the discard-wall directive, got: %s", reason)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(data), "pretooluse-denied:discard-bash") {
		t.Fatalf("the denial must be counted under its own policy, got:\n%s", data)
	}
}

// The bug report: the SAME words, sitting inside a quoted --body argument to
// `gh pr create`, are data. No git command ran, so the call must flow.
func TestRun_PreToolUse_AllowsDiscardWordsInsideAQuotedArgument(t *testing.T) {
	gateConfigDir(t)

	cmd := `gh pr create --body "$(printf '## Summary\n- discard git checkout -- file text here\n')"`
	var out, errb bytes.Buffer
	stdin := strings.NewReader(discardBashPayload(t, cmd))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 (allowed) — the words are data, not a command\nstdout:%s", code, out.String())
	}
}
