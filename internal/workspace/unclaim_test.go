package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnclaimPlan_ResolvesAndRenders(t *testing.T) {
	repo, branch := claimRepo(t) // aphrollo-web repo with feat/x prepared
	devclaim := t.TempDir()
	t.Setenv("APHROLLO_DEVCLAIM_DIR", devclaim)

	tg, err := ResolveTarget(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := UnclaimPlan(tg, "") // svc derived from web repo name
	if err != nil {
		t.Fatalf("UnclaimPlan: %v", err)
	}
	if u.Service != "rlndx" || u.RepoKey != "web" {
		t.Errorf("got service=%q key=%q, want rlndx/web", u.Service, u.RepoKey)
	}
	if u.MainRepo != repo {
		t.Errorf("MainRepo = %q, want %q", u.MainRepo, repo)
	}
	dry := u.Render(false)
	for _, want := range []string{"dev-rlndx", "repoint", repo, "restart dev-rlndx", "--dry"} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry-run render missing %q:\n%s", want, dry)
		}
	}
}

func TestUnclaimPlan_BadSvcRejected(t *testing.T) {
	repo, branch := claimRepo(t)
	t.Setenv("APHROLLO_DEVCLAIM_DIR", t.TempDir())
	tg, _ := ResolveTarget(repo, branch, "")
	if _, err := UnclaimPlan(tg, "postgres"); err == nil {
		t.Fatal("expected error for a disallowed service")
	}
}

// TestUnclaim_Apply_E2E claims (points the symlink at the worktree) then
// unclaims, asserting the symlink is restored to the main clone and the
// privileged restart fires with the exact unit — via the same systemctl fake
// the claim test uses, so no sudo or real dev tier is touched.
func TestUnclaim_Apply_E2E(t *testing.T) {
	repo, branch := claimRepo(t)
	devclaim := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(bin, "restart.txt")
	fake := fakeSystemctl(t, bin, marker)
	t.Setenv("APHROLLO_DEVCLAIM_DIR", devclaim)
	t.Setenv("APHROLLO_SYSTEMCTL", fake)
	t.Setenv("APHROLLO_DEV_SUDO", "0")
	t.Setenv("APHROLLO_SPACES", t.TempDir())

	tg, err := ResolveTarget(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	// Pre-point the symlink at the worktree (the state a prior claim leaves).
	if err := repointSymlink(filepath.Join(devclaim, "web"), tg.Worktree); err != nil {
		t.Fatal(err)
	}

	u, err := UnclaimPlan(tg, "rlndx")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := u.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	got, err := os.Readlink(filepath.Join(devclaim, "web"))
	if err != nil {
		t.Fatalf("symlink missing: %v", err)
	}
	if got != repo {
		t.Errorf("symlink -> %q, want main clone %q", got, repo)
	}
	rec, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("restart not invoked: %v", err)
	}
	if strings.TrimSpace(string(rec)) != "restart aphrollo-dev-rlndx.service" {
		t.Errorf("restart called with %q", strings.TrimSpace(string(rec)))
	}

	// Idempotent: now that the symlink already points at the main clone, a second
	// plan reports the repoint as skipped (but still restarts).
	u2, _ := UnclaimPlan(tg, "rlndx")
	if !strings.Contains(u2.Render(false), "skip") {
		t.Errorf("re-unclaim should skip the repoint:\n%s", u2.Render(false))
	}
}
