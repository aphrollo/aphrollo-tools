package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// The package's fixture repos never get git's detached post-commit
// maintenance, which would outlive the test and race its temp-dir cleanup.
func TestFixtureGit_RunsNoBackgroundMaintenance(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "gc.auto", "1"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cmd := exec.Command("git", "config", "--get", "gc.auto")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "0" {
		t.Fatalf("gc.auto = %q, want 0 whatever the repo's own config says", got)
	}
}
