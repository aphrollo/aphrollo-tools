package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// apiClaimRepo builds an aphrollo-api-named repo (so svc derives to "api") with
// one prepared worktree on feat/x. When withMigrations, a migrations/ dir is
// committed so it lands in the worktree. Returns repo path + branch. git is a
// hard dependency of these e2e tests (present in CI/dev); a missing git fails
// loudly rather than silently skipping.
func apiClaimRepo(t *testing.T, withMigrations bool) (string, string) {
	t.Helper()
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo := filepath.Join(t.TempDir(), "aphrollo-api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	gitRun("init", "-q", "-b", "main")
	gitRun("config", "user.email", "t@t")
	gitRun("config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644)
	if withMigrations {
		if err := os.MkdirAll(filepath.Join(repo, "migrations"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(repo, "migrations", "0001_init.sql"),
			[]byte("-- +goose Up\nSELECT 1;\n"), 0o644)
	}
	gitRun("add", ".")
	gitRun("commit", "-qm", "init")

	plan, err := BuildPlan(Request{Repo: repo, Branch: "feat/x", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("prepare Apply: %v\n%s", err, errb.String())
	}
	return repo, "feat/x"
}

// recorderBin writes a fake executable that appends its argv to a shared log,
// so a test can assert both WHICH external command ran and in what ORDER.
// recorderBin writes a fake executable at dir/name that appends "<name> <args>"
// to log on every call. POSIX: a shebang shell script. Windows can't run one
// directly (no shebang dispatch through CreateProcess, and Go's os/exec
// refuses a file with no PATHEXT-recognized extension even given a full
// path) — the fake is a .bat with the equivalent one-liner instead.
func recorderBin(t *testing.T, dir, name, log string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, name+".bat")
		if err := os.WriteFile(p, []byte("@echo off\r\necho "+name+" %* >> "+log+"\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \""+name+" $@\" >> "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestClaim_ApiMigrate_RunsGooseBeforeRestart drives an api claim and asserts the
// dev-DB `goose up` runs (with the worktree's migrations dir + the configured
// DSN) and runs BEFORE the privileged restart — both via fakes, no real DB or
// systemctl.
func TestClaim_ApiMigrate_RunsGooseBeforeRestart(t *testing.T) {
	repo, branch := apiClaimRepo(t, true)
	bin := t.TempDir()
	log := filepath.Join(bin, "order.log")
	goose := recorderBin(t, bin, "goose", log)
	systemctl := recorderBin(t, bin, "systemctl", log)

	t.Setenv("APHROLLO_DEVCLAIM_DIR", t.TempDir())
	t.Setenv("APHROLLO_GOOSE_BIN", goose)
	t.Setenv("APHROLLO_DEV_DB_URL", "postgres://sentinel/devdb")
	t.Setenv("APHROLLO_SYSTEMCTL", systemctl)
	t.Setenv("APHROLLO_DEV_SUDO", "0")
	t.Setenv("APHROLLO_SPACES", t.TempDir())

	c, err := ClaimPlan(repo, branch, "api", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}

	rec, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("nothing recorded: %v", err)
	}
	s := string(rec)
	if !strings.Contains(s, "goose -dir "+filepath.Join(c.Worktree, "migrations")+" postgres postgres://sentinel/devdb up") {
		t.Errorf("goose not invoked with the worktree migrations + dev DSN:\n%s", s)
	}
	gi, ri := strings.Index(s, "goose "), strings.Index(s, "systemctl ")
	if gi < 0 || ri < 0 || gi > ri {
		t.Errorf("goose up must run BEFORE the dev-api restart:\n%s", s)
	}
}

func TestClaim_ApiMigrate_NoMigrateFlagSkips(t *testing.T) {
	repo, branch := apiClaimRepo(t, true)
	bin := t.TempDir()
	log := filepath.Join(bin, "order.log")
	t.Setenv("APHROLLO_DEVCLAIM_DIR", t.TempDir())
	t.Setenv("APHROLLO_GOOSE_BIN", recorderBin(t, bin, "goose", log))
	t.Setenv("APHROLLO_SYSTEMCTL", recorderBin(t, bin, "systemctl", log))
	t.Setenv("APHROLLO_DEV_SUDO", "0")
	t.Setenv("APHROLLO_SPACES", t.TempDir())

	c, err := ClaimPlan(repo, branch, "api", "", true /*noMigrate*/)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); strings.Contains(string(data), "goose ") {
		t.Errorf("--no-migrate must skip goose, but it ran:\n%s", data)
	}
}

func TestClaim_ApiMigrate_NoMigrationsDir(t *testing.T) {
	repo, branch := apiClaimRepo(t, false) // no migrations/ committed
	t.Setenv("APHROLLO_DEVCLAIM_DIR", t.TempDir())
	c, err := ClaimPlan(repo, branch, "api", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Render(false), "no migrations/ dir") {
		t.Errorf("a repo without migrations/ should skip the goose step:\n%s", c.Render(false))
	}
}

func TestBuildFirstNote(t *testing.T) {
	wt := t.TempDir()
	// rlndx + unbuilt → advisory.
	if buildFirstNote("rlndx", wt) == "" {
		t.Error("an unbuilt rlndx worktree should get a build-first advisory")
	}
	// api → never.
	if buildFirstNote("api", wt) != "" {
		t.Error("api claims should not get the web build-first advisory")
	}
	// rlndx already built (.svelte-kit present) → no nag.
	if err := os.MkdirAll(filepath.Join(wt, ".svelte-kit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if buildFirstNote("rlndx", wt) != "" {
		t.Error("a built rlndx worktree (.svelte-kit present) should not be nagged")
	}
}

func TestGooseBinAndDevDBURL_EnvOverride(t *testing.T) {
	t.Setenv("APHROLLO_GOOSE_BIN", "/custom/goose")
	if gooseBin() != "/custom/goose" {
		t.Errorf("gooseBin should honor APHROLLO_GOOSE_BIN, got %q", gooseBin())
	}
	t.Setenv("APHROLLO_DEV_DB_URL", "postgres://x/y")
	if devDBURL() != "postgres://x/y" {
		t.Errorf("devDBURL should honor APHROLLO_DEV_DB_URL, got %q", devDBURL())
	}
}
