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
		// Substring matching mis-mapped these: "webhooks" merely contains "web",
		// "api-gateway" merely contains "api". Neither is a dev-tier repo.
		"aphrollo-webhooks": "",
		"webhooks":          "",
		"api-gateway":       "",
	}
	for name, want := range cases {
		if got := deriveService(name); got != want {
			t.Errorf("deriveService(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRepoKeyForSvc(t *testing.T) {
	if repoKeyForSvc("rlndx") != "web" {
		t.Error("rlndx should map to the web claim key")
	}
	if repoKeyForSvc("api") != "api" {
		t.Error("api should map to the api claim key")
	}
}

// claimRepo builds a web-named git repo (so service derivation bites) with one
// prepared worktree, and points GIT_CONFIG_GLOBAL at a throwaway. Returns the
// repo path and the prepared branch.
func claimRepo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Isolate git BEFORE the first commit: drop the global core.hooksPath
	// (aphrollo tdd gate) so it can't recurse into this fixture's setup commit,
	// and drop the hook's GIT_DIR/GIT_INDEX_FILE so git targets this throwaway,
	// not the real repo.
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
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

func TestClaimPlan_ResolvesAndRenders(t *testing.T) {
	repo, branch := claimRepo(t)
	devclaim := t.TempDir()
	t.Setenv("APHROLLO_DEVCLAIM_DIR", devclaim)
	t.Setenv("APHROLLO_DEV_BIN", "/opt/fake/aphrollo-dev")
	t.Setenv("APHROLLO_DEV_SUDO", "0")

	c, err := ClaimPlan(repo, branch, "", "", false) // svc derived from web repo name
	if err != nil {
		t.Fatalf("ClaimPlan: %v", err)
	}
	if c.Service != "rlndx" || c.RepoKey != "web" {
		t.Errorf("got service=%q key=%q, want rlndx/web", c.Service, c.RepoKey)
	}
	if c.Symlink != filepath.Join(devclaim, "web") {
		t.Errorf("symlink = %q, want %s/web", c.Symlink, devclaim)
	}
	dry := c.Render(false)
	for _, want := range []string{"dev-rlndx", "repoint", c.Worktree, "restart rlndx", "--dry"} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry-run render missing %q:\n%s", want, dry)
		}
	}
}

func TestClaimPlan_ExplicitSvcOverride(t *testing.T) {
	repo, branch := claimRepo(t)
	t.Setenv("APHROLLO_DEVCLAIM_DIR", t.TempDir())
	c, err := ClaimPlan(repo, branch, "api", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Service != "api" || c.RepoKey != "api" {
		t.Errorf("explicit --svc not honored: service=%q key=%q", c.Service, c.RepoKey)
	}
}

func TestClaimPlan_BadSvcRejected(t *testing.T) {
	repo, branch := claimRepo(t)
	if _, err := ClaimPlan(repo, branch, "postgres", "", false); err == nil {
		t.Fatal("expected error for disallowed service")
	}
}

func TestClaimPlan_MissingWorktree_PointsToPrepare(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t) // no worktree prepared
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	_, err := ClaimPlan(repo, "never-prepared", "rlndx", "", false)
	if err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("expected a missing-worktree error pointing at create, got: %v", err)
	}
}

func TestClaimPlan_CannotDeriveSvc(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	parent := t.TempDir()
	repo := filepath.Join(parent, "aphrollo-lens") // no web/api token
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

	_, err := ClaimPlan(repo, "br", "", "", false)
	if err == nil || !strings.Contains(err.Error(), "--svc") {
		t.Fatalf("expected a derive failure asking for --svc, got: %v", err)
	}
}

// TestClaim_Apply_E2E drives the full claim: repoint the symlink (real, in a
// temp .devclaim) and restart via dev.Restart, whose systemctl is faked to a
// recorder — proving the symlink lands on the worktree and the privileged atom
// is invoked with the exact unit, with no sudo or real dev tier. APHROLLO_SPACES
// is isolated so dev.Restart's vite-cache clear can't touch real files.
func TestClaim_Apply_E2E(t *testing.T) {
	repo, branch := claimRepo(t)
	devclaim := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(bin, "restart.txt")
	fake := fakeSystemctl(t, bin, marker)
	t.Setenv("APHROLLO_DEVCLAIM_DIR", devclaim)
	t.Setenv("APHROLLO_SYSTEMCTL", fake)
	t.Setenv("APHROLLO_DEV_SUDO", "0")
	t.Setenv("APHROLLO_SPACES", t.TempDir())

	c, err := ClaimPlan(repo, branch, "rlndx", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}

	// Symlink now resolves to the worktree.
	got, err := os.Readlink(filepath.Join(devclaim, "web"))
	if err != nil {
		t.Fatalf("claim symlink not created: %v", err)
	}
	if got != c.Worktree {
		t.Errorf("symlink -> %q, want %q", got, c.Worktree)
	}
	// The privileged restart hit the exact dev unit (via dev.Restart).
	rec, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("restart not invoked: %v", err)
	}
	if strings.TrimSpace(string(rec)) != "restart aphrollo-dev-rlndx.service" {
		t.Errorf("restart called with %q, want %q", strings.TrimSpace(string(rec)), "restart aphrollo-dev-rlndx.service")
	}

	// Idempotent: a second plan sees the symlink already points here.
	c2, err := ClaimPlan(repo, branch, "rlndx", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c2.Render(false), "already points here") {
		t.Errorf("re-claim should report the symlink already points here:\n%s", c2.Render(false))
	}
}

func TestRepointSymlink_ReplacesExisting(t *testing.T) {
	d := t.TempDir()
	link := filepath.Join(d, "web")
	if err := os.Symlink("/old/target", link); err != nil {
		t.Fatal(err)
	}
	if err := repointSymlink(link, "/new/target"); err != nil {
		t.Fatalf("repointSymlink: %v", err)
	}
	// Windows' reparse-point symlink target always round-trips through
	// Readlink with native (backslash) separators, regardless of what was
	// passed to Symlink; ToSlash makes the assertion separator-agnostic.
	got, _ := os.Readlink(link)
	if filepath.ToSlash(got) != "/new/target" {
		t.Errorf("symlink -> %q, want /new/target", got)
	}
}
