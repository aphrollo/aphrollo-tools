package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The hook is where a denial actually happens, so it is where the record has
// to be written — a Decision the CLI drops on the floor leaves the same blind
// spot the logging was added to close.
func TestRun_TDD_PreToolUseDenialIsRecorded(t *testing.T) {
	// Run from a directory that is not a checkout of anything. The hook
	// applies the primary-checkout policy BEFORE it looks at the content, so
	// with the suite's own working directory inherited this test recorded
	// pretooluse-denied:primary-checkout and never reached the detector it is
	// about. That made it pass from a lane worktree and fail from the primary
	// checkout — which is exactly where the merge gate runs it, so it blocked
	// every merge touching this package.
	t.Chdir(t.TempDir())
	cfg := gateConfigDir(t)
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"a_test.go","content":"assert x == x"}}`)

	if code := Run([]string{"tdd", "pretooluse"}, stdin, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (blocked)", code)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(data), "pretooluse-denied:tautology") {
		t.Fatalf("the denial must name its policy in gate.log, got:\n%s", data)
	}
}

// The wall sits in preToolUseWalls, so the hook itself refuses a shell write
// of a source file in a managed repo and names the policy in gate.log.
func TestRun_TDD_PreToolUseRefusesAShellSourceWrite(t *testing.T) {
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})
	t.Chdir(dir)
	cfg := gateConfigDir(t)
	var out, errb bytes.Buffer
	payload := `{"tool_name":"Bash","session_id":"s-wire","cwd":` + strconv.Quote(dir) +
		`,"tool_input":{"command":"echo package x > x.go"}}`

	if code := Run([]string{"tdd", "pretooluse"}, strings.NewReader(payload), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (blocked); stdout %q", code, out.String())
	}
	if !strings.Contains(out.String(), "write x.go with Edit/Write, not Bash") {
		t.Fatalf("refusal text missing: %q", out.String())
	}
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil || !strings.Contains(string(data), "pretooluse-denied:source-bash") {
		t.Fatalf("gate.log must name the source-bash policy: %v\n%s", err, data)
	}
}
