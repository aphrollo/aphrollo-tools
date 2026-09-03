package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// primaryWorktreeRepo builds a repo on `main` with one linked worktree and
// returns the primary checkout and the worktree.
func primaryWorktreeRepo(t *testing.T) (primary, linked string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		// skip-ok: git is the subject here, not a dependency that could be faked
		t.Skip("git not available")
	}
	isolateGit(t)
	primary = t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(primary, "init", "-q")
	run(primary, "config", "user.email", "t@example.com")
	run(primary, "config", "user.name", "t")
	run(primary, "checkout", "-q", "-B", "main")
	if err := os.WriteFile(filepath.Join(primary, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(primary, "add", "-A")
	run(primary, "commit", "-q", "-m", "init")
	linked = filepath.Join(t.TempDir(), "lane")
	run(primary, "worktree", "add", "-q", "-b", "lane/x", linked)
	return primary, linked
}

func primaryEditPayload(t *testing.T, path string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  "Write",
		"session_id": "cli-primary",
		"tool_input": map[string]any{"file_path": path, "content": "package main\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// denyReason reads the reason out of a PreToolUse deny envelope, so the
// assertions read the text a session is shown rather than its JSON escaping.
func denyReason(t *testing.T, payload []byte) string {
	t.Helper()
	var out struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("hook output is not a deny envelope: %v; payload: %s", err, payload)
	}
	return out.Reason
}

func TestRun_PreToolUse_DeniesAWriteIntoThePrimaryCheckout(t *testing.T) {
	cfg := gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)

	var out, errb bytes.Buffer
	stdin := strings.NewReader(primaryEditPayload(t, filepath.Join(primary, "main.go")))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied)\nstdout:%s", code, out.String())
	}
	reason := denyReason(t, out.Bytes())
	if !strings.Contains(reason, "primary checkout is merge-only") {
		t.Fatalf("the deny must name the rule and its escape, got: %s", reason)
	}
	if !strings.Contains(reason, "git worktree add -b lane/<name>") {
		t.Fatalf("the deny must carry the runnable recipe, got: %s", reason)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(data), "pretooluse-denied:primary-checkout") {
		t.Fatalf("the denial must be counted under its own policy, got:\n%s", data)
	}
}

func TestRun_PreToolUse_AllowsAWriteIntoALinkedWorktree(t *testing.T) {
	gateConfigDir(t)
	_, linked := primaryWorktreeRepo(t)

	var out, errb bytes.Buffer
	stdin := strings.NewReader(primaryEditPayload(t, filepath.Join(linked, "main.go")))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 0 {
		t.Fatalf("a worktree write is the happy path; exit = %d\nstdout:%s", code, out.String())
	}
}

func TestRun_PreToolUse_DeniesABashWriteIntoThePrimaryCheckout(t *testing.T) {
	gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)

	payload, err := json.Marshal(map[string]any{
		"tool_name":   "Bash",
		"session_id":  "cli-primary-bash",
		"tool_use_id": "toolu_1",
		"cwd":         primary,
		"tool_input":  map[string]any{"command": "echo hi >> main.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(string(payload)), &out, &errb); code != 2 {
		t.Fatalf("a shell write into the primary checkout must be denied; exit = %d\nstdout:%s", code, out.String())
	}
	if reason := denyReason(t, out.Bytes()); !strings.Contains(reason, "primary checkout is merge-only") {
		t.Fatalf("the deny must name the rule, got: %s", reason)
	}
}
