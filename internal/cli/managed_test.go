package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestRun_GateInit_InstallsTheManagedSkillsAndAgents pins the wiring: without
// it the templates are bytes nobody installs. The bytes themselves are checked
// in the tdd package; what this proves is that init reaches them and that
// --uninstall takes them back.
func TestRun_GateInit_InstallsTheManagedSkillsAndAgents(t *testing.T) {
	t.Chdir(t.TempDir())
	isolateGit(t)
	cfg := t.TempDir()

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "init", "--config-dir", cfg, "--no-git"},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}

	managed := map[string]string{
		"skills/sdd/SKILL.md":  tdd.SDDSkill(),
		"agents/builder.md":    managedAgent(t, "builder"),
		"agents/reviewer.md":   managedAgent(t, "reviewer"),
		"agents/researcher.md": managedAgent(t, "researcher"),
	}
	for rel, want := range managed {
		got, err := os.ReadFile(filepath.Join(cfg, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s not installed: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s is not the template the binary carries", rel)
		}
	}

	out.Reset()
	if code := Run([]string{"gate", "init", "--config-dir", cfg, "--no-git", "--uninstall"},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	for rel := range managed {
		if _, err := os.Stat(filepath.Join(cfg, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s survived --uninstall: %v", rel, err)
		}
	}
}

func managedAgent(t *testing.T, name string) string {
	t.Helper()
	body, ok := tdd.ManagedAgent(name)
	if !ok {
		t.Fatalf("no managed agent named %q", name)
	}
	return body
}
