package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #871: with mutants-before-pr declared, `aphrollo gate pretooluse`
// refuses a Bash command that opens a PR directly and names the verb that
// measures the lane first. `aphrollo workspace pr` runs gh as its own
// subprocess, so its own call never meets this hook.
func directPRPayload(t *testing.T, cwd, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":   "Bash",
		"session_id":  "cli-direct-pr",
		"tool_use_id": "toolu_1",
		"cwd":         cwd,
		"tool_input":  map[string]any{"command": cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mutantsBeforePRRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\nmutants-before-pr = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRun_PreToolUse_DeniesADirectPRCreate(t *testing.T) {
	cfg := gateConfigDir(t)
	dir := mutantsBeforePRRepo(t)

	var out, errb bytes.Buffer
	stdin := strings.NewReader(directPRPayload(t, dir, "git push -u origin lane/x && gh pr create --fill"))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if reason := denyReason(t, out.Bytes()); !strings.Contains(reason, "aphrollo workspace pr") {
		t.Fatalf("the deny must name `aphrollo workspace pr`, got: %s", reason)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(data), "pretooluse-denied:direct-pr-open") {
		t.Fatalf("the denial must be counted under its own policy, got:\n%s", data)
	}
}

func TestRun_PreToolUse_AllowsTheWorkspacePRVerb(t *testing.T) {
	gateConfigDir(t)
	dir := mutantsBeforePRRepo(t)

	var out, errb bytes.Buffer
	stdin := strings.NewReader(directPRPayload(t, dir, `aphrollo workspace pr --title "t" --body "b"`))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 (allowed)\nstdout:%s", code, out.String())
	}
	if strings.Contains(out.String(), "mutants-before-pr") {
		t.Fatalf("the workspace verb must meet no refusal, got: %s", out.String())
	}
}
