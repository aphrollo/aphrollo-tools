package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate's whole record — gate.log, the mechanical cache, the receipts, every
// live session file — lived under the old name. A rename that abandoned it
// would silently throw the pipeline's history away, so the dir is MOVED once.
func TestStateDirMovesThePreRenameDirectoryOnce(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", base)
	old := filepath.Join(base, "tdd-state")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "gate.log"), []byte("history\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := stateDir()
	if filepath.Base(dir) != "gate-state" {
		t.Fatalf("stateDir = %q, want the gate-state dir", dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, "gate.log"))
	if err != nil || string(data) != "history\n" {
		t.Fatalf("gate.log did not survive the move: %q (%v)", data, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old directory must be moved, not copied")
	}
}

func TestStateDirLeavesAnExistingGateStateDirAlone(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", base)
	current := filepath.Join(base, "gate-state")
	old := filepath.Join(base, "tdd-state")
	for _, d := range []string{current, old} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(current, "gate.log"), []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "gate.log"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(stateDir(), "gate.log"))
	if err != nil || string(data) != "current\n" {
		t.Fatalf("an existing gate-state dir must win: %q (%v)", data, err)
	}
}

// A box carrying hooks written before the rename must end up with ONE hook per
// event, not the new one stacked beside the old.
func TestPatchSettingsReplacesAPreRenameHookInsteadOfStackingOne(t *testing.T) {
	existing := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write|MultiEdit|NotebookEdit",
	  "hooks":[{"type":"command","command":"\"/usr/local/bin/aphrollo\" tdd pretooluse","timeout":10}]}]}}`)
	out, changed, err := PatchSettings(existing, "/usr/local/bin/aphrollo")
	if err != nil || !changed {
		t.Fatalf("PatchSettings: changed=%v err=%v", changed, err)
	}
	text := string(out)
	if strings.Contains(text, "tdd pretooluse") {
		t.Errorf("the pre-rename hook survived:\n%s", text)
	}
	// Three, one per matcher: the edit tools, Bash, and PowerShell. More than
	// that means the pre-rename entry was stacked beside the new ones rather
	// than replaced.
	if strings.Count(text, "gate pretooluse") != 3 {
		t.Errorf("want one wired PreToolUse hook per matcher (edit tools, Bash, PowerShell):\n%s", text)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
}

func TestGitShimsInvokeTheGateSubcommand(t *testing.T) {
	if got := shim("/usr/local/bin/aphrollo", "precommit"); !strings.Contains(got, `"/usr/local/bin/aphrollo" gate precommit`) {
		t.Errorf("per-repo shim = %q", got)
	}
	if got := binShim("/usr/local/bin/aphrollo", "premergecommit", ""); !strings.Contains(got, `"/usr/local/bin/aphrollo" gate premergecommit`) {
		t.Errorf("global shim = %q", got)
	}
}

func TestGateControlCommandAnswersBothSpellings(t *testing.T) {
	for _, prompt := range []string{"/gate status", "/tdd status", "/gate", "/tdd"} {
		if !isGateCommand(prompt) {
			t.Errorf("%q must be recognised as the control command", prompt)
		}
	}
	if isGateCommand("/gateway to nowhere") {
		t.Error("a prompt that merely starts with the name is not the command")
	}
}
