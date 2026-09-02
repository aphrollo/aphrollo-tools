package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hook is where a denial actually happens, so it is where the record has
// to be written — a Decision the CLI drops on the floor leaves the same blind
// spot the logging was added to close.
func TestRun_TDD_PreToolUseDenialIsRecorded(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
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
