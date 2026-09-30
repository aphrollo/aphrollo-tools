package gitenv

import (
	"os/exec"
	"strings"
	"testing"
)

// After DisableMaintenance, git reports auto maintenance off whatever the
// repo's own config says: the settings ride the environment, which outranks
// repo config.
func TestDisableMaintenance_OutranksTheReposOwnConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "maintenance.auto", "true"},
		{"config", "gc.auto", "1"},
		{"config", "gc.autoDetach", "true"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	DisableMaintenance(t.Setenv)

	for key, want := range map[string]string{
		"maintenance.auto": "false",
		"gc.auto":          "0",
		"gc.autoDetach":    "false",
	} {
		cmd := exec.Command("git", "config", "--get", key)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("git config %s = %q, want %q", key, got, want)
		}
	}
}
