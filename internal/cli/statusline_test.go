package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun_GateStatusline_PrintsOneBadgeLine pins the shape the statusline
// contract needs: exactly one line on stdout, exit 0, nothing on stderr. A
// second line is rendered as part of the prompt, and a non-zero exit makes
// Claude Code drop the statusline entirely.
func TestRun_GateStatusline_PrintsOneBadgeLine(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "statusline"},
		strings.NewReader(`{"session_id":"s1","cwd":"`+t.TempDir()+`"}`), &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("statusline must print nothing on stderr, got: %q", errb.String())
	}
	got := out.String()
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Fatalf("statusline must print exactly one line, got %q", got)
	}
	if !strings.Contains(got, "[aphrollo]") {
		t.Fatalf("statusline = %q, want the badge", got)
	}
}

// TestRun_GateInit_WiresTheStatusLineAndClearsRetiredHooks pins the wiring
// between init and the two pieces above: without it the badge is code nobody
// invokes, and the Node plugin's hook scripts stay on disk beside it.
func TestRun_GateInit_WiresTheStatusLineAndClearsRetiredHooks(t *testing.T) {
	t.Chdir(t.TempDir())
	isolateGit(t)
	cfg := t.TempDir()
	hooks := filepath.Join(cfg, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(hooks, "tdd-post-edit.js")
	if err := os.WriteFile(retired, []byte("// node plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "init", "--config-dir", cfg, "--no-git"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	settings, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "gate statusline") {
		t.Errorf("settings.json carries no statusLine:\n%s", settings)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Errorf("the retired hook survived init: %v", err)
	}
	if !strings.Contains(out.String(), "removed the retired hook") {
		t.Errorf("init must report the removal, got:\n%s", out.String())
	}
}

// TestRun_GateStatusline_SurvivesAnEmptyPayload keeps the badge from being the
// thing that breaks a prompt render: it has nowhere to report an error.
func TestRun_GateStatusline_SurvivesAnEmptyPayload(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "statusline"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "[aphrollo]") {
		t.Fatalf("statusline = %q, want the badge", out.String())
	}
}
