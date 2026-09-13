package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `gate init` edits a source file in whatever repo the shell happens to be
// standing in — a side effect of the working directory, invisible in the
// command line. --repo names the target outright, and the run says which
// file it touched.
func TestGateInit_WritesTheBlockIntoTheNamedRepo(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1") // --git-hooks-dir below sits under t.TempDir()
	target := resolvedTempDir(t)
	gitInitRepo(t, target)
	writeFile(t, filepath.Join(target, "CLAUDE.md"), "# Project\n\nGuidance.\n")

	elsewhere := t.TempDir()
	gitInitRepo(t, elsewhere)
	writeFile(t, filepath.Join(elsewhere, "CLAUDE.md"), "# Other\n")
	t.Chdir(elsewhere)

	cfg := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "init", "--repo", target, "--config-dir", cfg,
		"--bin", fakeInstalledBin(t), "--git-hooks-dir", filepath.Join(t.TempDir(), "githooks"),
		"--cargo-shim-dir", filepath.Join(t.TempDir(), "bin", "cargo-queue")},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d\n%s%s", code, out.String(), errb.String())
	}

	if got := readFile(t, filepath.Join(target, "CLAUDE.md")); !strings.Contains(got, "<!-- aphrollo:begin -->") {
		t.Fatalf("--repo's CLAUDE.md was not written: %q", got)
	}
	if got := readFile(t, filepath.Join(elsewhere, "CLAUDE.md")); strings.Contains(got, "aphrollo:begin") {
		t.Fatalf("init wrote the CWD repo's CLAUDE.md despite --repo: %q", got)
	}
	if !strings.Contains(out.String(), filepath.Join(target, "CLAUDE.md")) {
		t.Errorf("init must name the file it edited, got: %q", out.String())
	}
}

// The help text has to say init edits a file in a repo, or the side effect
// stays a surprise to whoever reads --help before running it.
func TestGateUsage_SaysInitWritesTheManagedBlock(t *testing.T) {
	if !strings.Contains(gateUsage, "--repo") || !strings.Contains(gateUsage, "CLAUDE.md") {
		t.Fatalf("gate usage must document --repo and the CLAUDE.md write:\n%s", gateUsage)
	}
}
