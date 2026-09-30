package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// realHookFiles lists what a git dir's hooks dir holds besides the *.sample
// files `git init` seeds.
func realHookFiles(t *testing.T, hooksDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(hooksDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sample") {
			names = append(names, e.Name())
		}
	}
	return names
}

type sharedHooksFixture struct {
	primary, lane, cfg, hooksDir, shimDir string
}

func newSharedHooksFixture(t *testing.T) sharedHooksFixture {
	t.Helper()
	primary, lane := primaryWorktreeRepo(t)
	return sharedHooksFixture{
		primary:  primary,
		lane:     lane,
		cfg:      t.TempDir(),
		hooksDir: filepath.Join(t.TempDir(), "githooks"),
		shimDir:  filepath.Join(t.TempDir(), "cargo-queue"),
	}
}

func (f sharedHooksFixture) install(t *testing.T, extra ...string) (int, string, string) {
	t.Helper()
	args := append([]string{"install", "--repo", f.lane, "--config-dir", f.cfg,
		"--git-hooks-dir", f.hooksDir, "--cargo-shim-dir", f.shimDir}, extra...)
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// The control: without --no-git, install from a linked worktree writes the
// repo-level shims into the primary's shared hooks dir. The two tests below
// prove their "untouched" against a write that does happen.
func TestInstall_FromALinkedWorktreeWritesTheSharedHooksDirByDefault(t *testing.T) {
	f := newSharedHooksFixture(t)
	if code, out, errb := f.install(t, "--bin", fakeInstalledBin(t)); code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out, errb)
	}
	if len(realHookFiles(t, filepath.Join(f.primary, ".git", "hooks"))) == 0 {
		t.Fatal("a default install wrote no repo-level hook into the primary's .git/hooks")
	}
}

// Issue #1033: --no-git stopped the global gate and the queue shims but still
// wrote the repo-level shims into the git dir every worktree shares.
func TestInstall_NoGitFromALinkedWorktreeLeavesThePrimarysHooksUntouched(t *testing.T) {
	f := newSharedHooksFixture(t)
	if code, out, errb := f.install(t, "--no-git", "--bin", fakeInstalledBin(t)); code != 0 {
		t.Fatalf("install --no-git exit = %d\nstdout: %s\nstderr: %s", code, out, errb)
	}
	if got := realHookFiles(t, filepath.Join(f.primary, ".git", "hooks")); len(got) != 0 {
		t.Fatalf("install --no-git wrote %v into the primary's .git/hooks", got)
	}
}

// An installer that resolves its own path to a temp, go-build or worktree
// location must not wire hooks to it: the binary goes away and the gate with
// it. Naming it with --bin is the operator saying they mean it.
func TestInstall_RefusesAnUnstableDefaultBinaryAndWritesNothing(t *testing.T) {
	f := newSharedHooksFixture(t)
	scratch := fakeInstalledBin(t)
	fakeRunningAs(t, scratch, scratch)

	code, _, errb := f.install(t)
	if code == 0 {
		t.Fatal("install exit = 0 for a default binary in a temp dir, want a refusal")
	}
	if !strings.Contains(errb, "--bin") {
		t.Errorf("refusal does not say --bin names it explicitly:\n%s", errb)
	}
	if _, err := os.Stat(filepath.Join(f.cfg, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("session hooks were written despite the refusal (stat err = %v)", err)
	}
	if got := realHookFiles(t, filepath.Join(f.primary, ".git", "hooks")); len(got) != 0 {
		t.Errorf("repo-level hooks %v were written despite the refusal", got)
	}
	if _, err := os.Stat(f.shimDir); !os.IsNotExist(err) {
		t.Errorf("queue shims were written despite the refusal (stat err = %v)", err)
	}
}

// The same guard sits on the alias, which reaches the same writers.
func TestGateInit_RefusesAnUnstableDefaultBinary(t *testing.T) {
	f := newGateInitFixture(t)
	scratch := fakeInstalledBin(t)
	fakeRunningAs(t, scratch, scratch)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "init", "--config-dir", f.cfg, "--git-hooks-dir", f.hooks,
		"--cargo-shim-dir", f.shimDir}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("gate init exit = 0 for a default binary in a temp dir\nstdout: %s", out.String())
	}
	f.wroteNothing(t)
}

// The repo-level writer is reachable on its own too.
func TestGateInstall_RefusesAnUnstableDefaultBinaryWhenApplying(t *testing.T) {
	f := newSharedHooksFixture(t)
	scratch := fakeInstalledBin(t)
	fakeRunningAs(t, scratch, scratch)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "install", "--repo", f.lane}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatalf("gate install exit = 0 for a default binary in a temp dir\nstdout: %s", out.String())
	}
	if got := realHookFiles(t, filepath.Join(f.primary, ".git", "hooks")); len(got) != 0 {
		t.Errorf("repo-level hooks %v were written despite the refusal", got)
	}
}

// Without --apply gate install only prints its plan and writes nothing, so a
// default binary in a temp dir is no reason to refuse it.
func TestGateInstall_PrintsThePlanForAnUnstableDefaultBinaryWithoutApply(t *testing.T) {
	f := newSharedHooksFixture(t)
	scratch := fakeInstalledBin(t)
	fakeRunningAs(t, scratch, scratch)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "install", "--repo", f.lane, "--dry"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("gate install (dry) exit = %d\nstderr: %s", code, errb.String())
	}
	if got := realHookFiles(t, filepath.Join(f.primary, ".git", "hooks")); len(got) != 0 {
		t.Errorf("a dry plan wrote %v", got)
	}
}

// The narrow command: re-render the managed block and nothing else. It needs
// no binary, so it runs from a scratch-built one, and it touches neither the
// config dir, the global gate, the queue shims nor the shared hooks dir.
func TestInstall_ManagedBlockOnlyRendersTheBlockAndNothingElse(t *testing.T) {
	f := newSharedHooksFixture(t)
	claude := filepath.Join(f.lane, "CLAUDE.md")
	writeFile(t, claude, "# repo\n\n<!-- aphrollo:begin -->\nold, superseded\n<!-- aphrollo:end -->\n")
	scratch := fakeInstalledBin(t)
	fakeRunningAs(t, scratch, scratch)

	args := []string{"install", "--managed-block-only", "--repo", f.lane, "--config-dir", f.cfg,
		"--git-hooks-dir", f.hooksDir, "--cargo-shim-dir", f.shimDir}
	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("install --managed-block-only exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}

	got := readFile(t, claude)
	if strings.Contains(got, "old, superseded") || !strings.Contains(got, "## Working with the aphrollo gate") {
		t.Errorf("the block was not re-rendered:\n%s", got)
	}
	if !strings.Contains(got, "# repo\n") {
		t.Errorf("text outside the block was lost:\n%s", got)
	}
	if entries, _ := os.ReadDir(f.cfg); len(entries) != 0 {
		t.Errorf("the config dir was written: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(f.lane, ".ratchet", "README.md")); !os.IsNotExist(err) {
		t.Errorf("the law spec was written (stat err = %v)", err)
	}
	for _, dir := range []string{f.hooksDir, f.shimDir} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s was written (stat err = %v)", dir, err)
		}
	}
	if got := realHookFiles(t, filepath.Join(f.primary, ".git", "hooks")); len(got) != 0 {
		t.Errorf("the primary's .git/hooks gained %v", got)
	}
	if strings.Contains(out.String(), "features") {
		t.Errorf("the opt-in table was printed:\n%s", out.String())
	}
}

// A repo that keeps no CLAUDE.md gets no block from the narrow command either,
// unless --claude-md asks for one.
func TestInstall_ManagedBlockOnlyWritesAMissingClaudeMDOnlyWhenAsked(t *testing.T) {
	f := newSharedHooksFixture(t)
	claude := filepath.Join(f.lane, "CLAUDE.md")
	base := []string{"install", "--managed-block-only", "--repo", f.lane}

	var out, errb bytes.Buffer
	if code := Run(base, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(claude); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md was created without --claude-md (stat err = %v)", err)
	}
	if code := Run(append(base, "--claude-md"), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("--claude-md exit = %d\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(readFile(t, claude), tdd.ClaudeMDBlock(tdd.BlockFlags{})[:40]) {
		t.Errorf("--claude-md did not write the block:\n%s", readFile(t, claude))
	}
}
