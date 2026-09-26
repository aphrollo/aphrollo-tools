package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #879 item 3: in a repo declaring undercover = true, `aphrollo gate
// pretooluse` refuses a Bash command that would create a tell-named branch
// before it runs, and counts the denial under its own policy.
func TestRun_PreToolUse_DeniesATellBranchInAnUndercoverRepo(t *testing.T) {
	cfg := gateConfigDir(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\nundercover = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	stdin := strings.NewReader(directPRPayload(t, dir, "git switch -c lane/claude-fix"))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if reason := denyReason(t, out.Bytes()); !strings.Contains(reason, `"lane/claude-fix"`) {
		t.Fatalf("the deny must quote the branch name, got: %s", reason)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(data), "pretooluse-denied:undercover") {
		t.Fatalf("the denial must be counted under its own policy, got:\n%s", data)
	}
}
