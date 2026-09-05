package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstall_PerformsInitThenInstallWrites proves the merge: one `install`
// call wires the session hooks, the global git gate, AND --repo's own
// .git/hooks shims — all three, in one run, with no second command.
func TestInstall_PerformsInitThenInstallWrites(t *testing.T) {
	isolateGit(t)
	repo := t.TempDir()
	gitInitRepo(t, repo)

	cfg := t.TempDir()
	hooksDir := t.TempDir()
	shims := filepath.Join(t.TempDir(), "cargo-queue")
	args := []string{"install",
		"--repo", repo,
		"--bin", "/usr/local/bin/aphrollo",
		"--config-dir", cfg,
		"--git-hooks-dir", hooksDir,
		"--cargo-shim-dir", shims,
	}

	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}

	if _, err := os.Stat(filepath.Join(cfg, "settings.json")); err != nil {
		t.Errorf("settings.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-commit")); err != nil {
		t.Errorf("global git gate hook not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Errorf("the repo's own .git/hooks shim was not written: %v", err)
	}
}

// TestInstall_WritesTheSameBinaryIntoSessionHooksAndRepoShims guards the split
// `install --bin X` used to leave: the session hooks (settings.json) pointed
// at X, but the repo's own .git/hooks shims always baked in the RUNNING
// binary's path instead — one `install` call, two different binaries wired
// in, exactly what `gate doctor`'s doctorHookBinary check exists to catch.
func TestInstall_WritesTheSameBinaryIntoSessionHooksAndRepoShims(t *testing.T) {
	isolateGit(t)
	repo := t.TempDir()
	gitInitRepo(t, repo)

	cfg := t.TempDir()
	hooksDir := t.TempDir()
	shims := filepath.Join(t.TempDir(), "cargo-queue")
	// Forward-slashed so it round-trips byte-identical through shellPath's
	// backslash normalization — the test wants a literal match, not a
	// path-equivalence one.
	custom := strings.ReplaceAll(filepath.Join(t.TempDir(), "custom", "aphrollo.exe"), "\\", "/")

	args := []string{"install",
		"--repo", repo,
		"--bin", custom,
		"--config-dir", cfg,
		"--git-hooks-dir", hooksDir,
		"--cargo-shim-dir", shims,
	}
	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}

	settings, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	if !bytes.Contains(settings, []byte(custom)) {
		t.Errorf("settings.json does not contain --bin %q:\n%s", custom, settings)
	}

	shimBody, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", "pre-commit"))
	if err != nil {
		t.Fatalf("reading repo pre-commit shim: %v", err)
	}
	if !bytes.Contains(shimBody, []byte(custom)) {
		t.Errorf("repo pre-commit shim does not contain --bin %q:\n%s", custom, shimBody)
	}
	if running := defaultBinPath(); bytes.Contains(shimBody, []byte(running)) {
		t.Errorf("repo pre-commit shim contains the running binary %q instead of --bin %q:\n%s", running, custom, shimBody)
	}
}

// TestGateInitAndGateInstall_StillWork proves the old spellings are unchanged
// — same writes as always — and that the gate usage text now labels both as
// aliases of the merged top-level verb.
func TestGateInitAndGateInstall_StillWork(t *testing.T) {
	isolateGit(t)
	repo := t.TempDir()
	gitInitRepo(t, repo)

	cfg := t.TempDir()
	hooksDir := t.TempDir()
	shims := filepath.Join(t.TempDir(), "cargo-queue")

	var out, errb bytes.Buffer
	initArgs := []string{"gate", "init",
		"--repo", repo, "--bin", "/usr/local/bin/aphrollo",
		"--config-dir", cfg, "--git-hooks-dir", hooksDir, "--cargo-shim-dir", shims,
	}
	if code := Run(initArgs, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("gate init exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(cfg, "settings.json")); err != nil {
		t.Errorf("settings.json not written by gate init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-commit")); err != nil {
		t.Errorf("global git gate not written by gate init: %v", err)
	}

	out.Reset()
	errb.Reset()
	installArgs := []string{"gate", "install", "--repo", repo, "--apply"}
	if code := Run(installArgs, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("gate install exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Errorf("repo shim not written by gate install: %v", err)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"gate", "-h"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("gate -h exit = %d", code)
	}
	usage := out.String()
	for _, want := range []string{
		"install           (alias of aphrollo install; retiring next release)",
		"init              (alias of aphrollo install; retiring next release)",
		"issue             (alias of aphrollo issue; retiring next release)",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("gate usage must list %q as an alias, got:\n%s", want, usage)
		}
	}
}
