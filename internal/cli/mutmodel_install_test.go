package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// gateInstallRepo is a fresh repo for `gate install`, with git isolated so the
// write lands in the temp repo's own hooks dir and nowhere else.
func gateInstallRepo(t *testing.T) (repo, hook string) {
	t.Helper()
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo = t.TempDir()
	gitInitRepo(t, repo)
	return repo, filepath.Join(repo, ".git", "hooks", "pre-commit")
}

func runGateInstallVerb(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Run(append([]string{"gate", "install"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestGateInstall_DefaultWritesTheShims(t *testing.T) {
	repo, hook := gateInstallRepo(t)

	if code, _, errb := runGateInstallVerb("--repo", repo, "--bin", fakeInstalledBin(t)); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := os.Stat(hook); err != nil {
		t.Fatalf("the default run wrote no pre-commit shim: %v", err)
	}
}

func TestGateInstall_DryAfterOtherFlagsWritesNothing(t *testing.T) {
	repo, hook := gateInstallRepo(t)

	code, out, errb := runGateInstallVerb("--repo", repo, "--bin", fakeInstalledBin(t), "--dry")

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := os.Stat(hook); err == nil {
		t.Fatal("--dry wrote the pre-commit shim")
	}
	if !strings.Contains(out, "without --dry") {
		t.Errorf("the plan should say how to write: %s", out)
	}
}

func TestGateInstall_LegacyApplyStillWritesAndNotes(t *testing.T) {
	repo, hook := gateInstallRepo(t)

	code, _, errb := runGateInstallVerb("--repo", repo, "--bin", fakeInstalledBin(t), "--apply")

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := os.Stat(hook); err != nil {
		t.Fatalf("--apply no longer writes the shim: %v", err)
	}
	if !strings.Contains(errb, "aphrollo gate install: --apply is a no-op") {
		t.Errorf("stderr lacks the notice:\n%s", errb)
	}
}

func TestGateInstall_StrayArgumentIsRefusedAndWritesNothing(t *testing.T) {
	repo, hook := gateInstallRepo(t)

	code, _, errb := runGateInstallVerb("stray", "--repo", repo, "--bin", fakeInstalledBin(t))

	if code != 2 || !strings.Contains(errb, `unexpected argument "stray"`) {
		t.Fatalf("exit %d, stderr %q; want 2 naming the stray argument", code, errb)
	}
	if _, err := os.Stat(hook); err == nil {
		t.Fatal("a refused run wrote the shim")
	}
}

// sqlc regen takes its config as a positional and its other flags anywhere: a
// --repo written after the config must be the one searched.
func TestSqlcRegen_FlagAfterThePositionalIsParsed(t *testing.T) {
	empty := t.TempDir()
	var out, errb bytes.Buffer

	code := Run([]string{"sqlc", "regen", "sqlc.yaml", "--repo", empty}, strings.NewReader(""), &out, &errb)

	if code != 1 {
		t.Fatalf("exit %d, want 1 (no config); stderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), empty) {
		t.Fatalf("the --repo after the positional was not honoured:\n%s", errb.String())
	}
}

func TestSqlcRegen_LegacyApplyPrintsItsNotice(t *testing.T) {
	var out, errb bytes.Buffer

	Run([]string{"sqlc", "regen", "--repo", t.TempDir(), "--apply"}, strings.NewReader(""), &out, &errb)

	if !strings.Contains(errb.String(), "aphrollo sqlc regen: --apply is a no-op") {
		t.Fatalf("stderr lacks the notice:\n%s", errb.String())
	}
}

func TestSqlcRegen_UnknownFlagAfterThePositionalIsRefused(t *testing.T) {
	var out, errb bytes.Buffer

	code := Run([]string{"sqlc", "regen", "sqlc.yaml", "--bogus"}, strings.NewReader(""), &out, &errb)

	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr: %s", code, errb.String())
	}
}
