package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The merge-only primary cannot commit at all — writing the managed
// CLAUDE.md block there left the checkout permanently dirty with no commit
// able to clear it, which blocked workspace sync and left self-install to
// build off a stale tree. `gate init` must not fail the whole run over this:
// it prints the one notice and exits 0, same as every other already-current
// step.
func TestGateInit_PrintsOneNoticeAndExitsZeroInAMergeOnlyPrimary(t *testing.T) {
	primary, _ := primaryWorktreeRepo(t)

	cfg := t.TempDir()
	args := []string{"gate", "init", "--repo", primary, "--config-dir", cfg,
		"--bin", "/usr/local/bin/aphrollo",
		"--git-hooks-dir", filepath.Join(t.TempDir(), "githooks"),
		"--cargo-shim-dir", filepath.Join(t.TempDir(), "bin", "cargo-queue")}

	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}

	want := "gate init: CLAUDE.md managed block is behind the template in the merge-only primary; land it through a lane (aphrollo gate init --repo <lane>)"
	got := out.String()
	if n := strings.Count(got, want); n != 1 {
		t.Fatalf("notice line appears %d times, want exactly 1\nstdout:%s", n, got)
	}
}
