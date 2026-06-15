package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveService(t *testing.T) {
	cases := map[string]string{
		"aphrollo-web": "rlndx",
		"aphrollo-api": "api",
		"web":          "rlndx",
		"api":          "api",
		"askdoc":       "",
		"lens":         "",
	}
	for name, want := range cases {
		if got := deriveService(name); got != want {
			t.Errorf("deriveService(%q) = %q, want %q", name, got, want)
		}
	}
}

// claimRepo builds a git repo with one prepared worktree and returns (repo,
// branch). It points GIT_CONFIG_GLOBAL and APHROLLO_DEV_BIN at throwaways so no
// real global config or sudo is touched.
func claimRepo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Name the repo so service derivation (web -> rlndx) has something to bite.
	repo := filepath.Join(t.TempDir(), "aphrollo-web")
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
	gitRun("add", ".")
	gitRun("commit", "-qm", "init")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	plan, err := BuildPlan(Request{Repo: repo, Branch: "feat/x", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	return repo, "feat/x"
}

func TestClaimPlan_BuildsFencedCommand(t *testing.T) {
	repo, branch := claimRepo(t)
	t.Setenv("APHROLLO_DEV_BIN", "/opt/fake/aphrollo-dev")
	t.Setenv("APHROLLO_DEV_SUDO", "0") // deterministic: no sudo prefix in the assertion

	c, err := ClaimPlan(repo, branch, "", "") // svc derived
	if err != nil {
		t.Fatalf("ClaimPlan: %v", err)
	}
	if c.Service != "rlndx" {
		t.Errorf("derived service = %q, want rlndx", c.Service)
	}
	for _, want := range []string{"/opt/fake/aphrollo-dev", "claim", "rlndx", c.Worktree} {
		if !strings.Contains(c.Display, want) {
			t.Errorf("Display %q missing %q", c.Display, want)
		}
	}
	if strings.HasPrefix(c.Display, "sudo") {
		t.Errorf("APHROLLO_DEV_SUDO=0 should drop sudo prefix: %q", c.Display)
	}
}

func TestClaimPlan_SudoPrefixedByDefault(t *testing.T) {
	repo, branch := claimRepo(t)
	t.Setenv("APHROLLO_DEV_BIN", "/opt/fake/aphrollo-dev")
	os.Unsetenv("APHROLLO_DEV_SUDO")
	c, err := ClaimPlan(repo, branch, "rlndx", "")
	if err != nil {
		t.Fatal(err)
	}
	// Non-root test process => sudo is prefixed.
	if os.Geteuid() != 0 && !strings.HasPrefix(c.Display, "sudo ") {
		t.Errorf("expected sudo prefix for non-root: %q", c.Display)
	}
}

func TestClaimPlan_ExplicitSvcOverride(t *testing.T) {
	repo, branch := claimRepo(t)
	t.Setenv("APHROLLO_DEV_BIN", "/opt/fake/aphrollo-dev")
	c, err := ClaimPlan(repo, branch, "api", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Service != "api" {
		t.Errorf("explicit --svc not honored: got %q", c.Service)
	}
}

func TestClaimPlan_BadSvcRejected(t *testing.T) {
	repo, branch := claimRepo(t)
	if _, err := ClaimPlan(repo, branch, "postgres", ""); err == nil {
		t.Fatal("expected error for disallowed service")
	}
}

func TestClaimPlan_MissingWorktree_PointsToPrepare(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t) // no worktree prepared
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	_, err := ClaimPlan(repo, "never-prepared", "rlndx", "")
	if err == nil {
		t.Fatal("expected error for missing worktree")
	}
	if !strings.Contains(err.Error(), "prepare") {
		t.Errorf("error should point at prepare: %v", err)
	}
}

func TestClaimPlan_CannotDeriveSvc(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// repo basename has no web/api token => derivation fails, asks for --svc.
	parent := t.TempDir()
	repo := filepath.Join(parent, "aphrollo-lens")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git setup: %v\n%s", err, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644)
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "commit", "-qm", "i").Run()

	_, err := ClaimPlan(repo, "br", "", "")
	if err == nil || !strings.Contains(err.Error(), "--svc") {
		t.Fatalf("expected derive failure asking for --svc, got: %v", err)
	}
}

// TestClaim_Run_E2E drives Run against a fake dev script that records its argv,
// proving the wrapper invokes the fence with the right arguments — without sudo
// or a real dev tier.
func TestClaim_Run_E2E(t *testing.T) {
	repo, branch := claimRepo(t)
	bin := t.TempDir()
	marker := filepath.Join(bin, "called.txt")
	fake := filepath.Join(bin, "aphrollo-dev")
	script := "#!/bin/sh\necho \"$@\" > " + marker + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APHROLLO_DEV_BIN", fake)
	t.Setenv("APHROLLO_DEV_SUDO", "0")

	c, err := ClaimPlan(repo, branch, "rlndx", "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("fake dev script not invoked: %v", err)
	}
	want := "claim rlndx " + c.Worktree
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("fence called with %q, want %q", strings.TrimSpace(string(got)), want)
	}
}
