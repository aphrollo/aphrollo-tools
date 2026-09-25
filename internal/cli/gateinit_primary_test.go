package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const gateInitPrimaryNotice = "gate init: CLAUDE.md managed block is behind the template in the merge-only primary; land it through a lane (aphrollo install --repo <lane>)"

// The merge-only primary cannot commit at all — writing the managed
// CLAUDE.md block there left the checkout permanently dirty with no commit
// able to clear it, which blocked workspace sync and left self-install to
// build off a stale tree. `gate init` must not fail the whole run over this:
// it prints the one notice and exits 0, same as every other already-current
// step. The notice is only true when the block there is actually stale, so
// the primary's CLAUDE.md is seeded with an OLD block here — a current one
// is TestGateInit_PrintsNoNoticeWhenThePrimaryBlockIsCurrent's job.
func TestGateInit_PrintsOneNoticeAndExitsZeroInAMergeOnlyPrimary(t *testing.T) {
	primary, _ := primaryWorktreeRepo(t)
	writeFile(t, filepath.Join(primary, "CLAUDE.md"), "# repo\n\n<!-- aphrollo:begin -->\nold, superseded\n<!-- aphrollo:end -->\n")

	cfg := t.TempDir()
	args := []string{"gate", "init", "--repo", primary, "--config-dir", cfg,
		"--bin", fakeInstalledBin(t),
		"--git-hooks-dir", filepath.Join(t.TempDir(), "githooks"),
		"--cargo-shim-dir", filepath.Join(t.TempDir(), "bin", "cargo-queue")}

	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}

	got := out.String()
	if n := strings.Count(got, gateInitPrimaryNotice); n != 1 {
		t.Fatalf("notice line appears %d times, want exactly 1\nstdout:%s", n, got)
	}
}

// A primary whose managed block is already current must get no notice at
// all: the sentinel only fires when a write would actually change the file,
// and telling a session its up-to-date primary is "behind the template"
// would be a lie gate init has no business printing.
func TestGateInit_PrintsNoNoticeWhenThePrimaryBlockIsCurrent(t *testing.T) {
	primary, _ := primaryWorktreeRepo(t)
	shimDir := filepath.Join(t.TempDir(), "bin", "cargo-queue")
	claudePath := filepath.Join(primary, "CLAUDE.md")
	writeFile(t, claudePath, "# repo\n\n"+tdd.ClaudeMDBlock(tdd.BlockFlags{}))

	cfg := t.TempDir()
	args := []string{"gate", "init", "--repo", primary, "--config-dir", cfg,
		"--bin", fakeInstalledBin(t),
		"--git-hooks-dir", filepath.Join(t.TempDir(), "githooks"),
		"--cargo-shim-dir", shimDir}

	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}

	if strings.Contains(out.String(), gateInitPrimaryNotice) {
		t.Fatalf("notice printed for a primary whose block is already current:\n%s", out.String())
	}

	after, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "# repo\n\n"+tdd.ClaudeMDBlock(tdd.BlockFlags{}) {
		t.Fatalf("CLAUDE.md was rewritten despite being current:\n%s", after)
	}
}
