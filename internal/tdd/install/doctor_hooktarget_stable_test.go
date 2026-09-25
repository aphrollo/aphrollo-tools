package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A runnable binary is not necessarily a DURABLE one: a shim pointed at a
// path this box's own deploy convention (deploy/deploy-prod.sh) stages under
// a directory literally named "releases" is one the very next deploy prunes,
// even though it is perfectly runnable the moment this check reads it.
// doctorGitHookBinary's own runnability probe cannot see that — nothing
// about the file is wrong YET. This check exists because of the 2026-09-25
// incident: `aphrollo install` (no --bin) wrote the global git hooks and
// the session hooks at .../releases/<ts>-<sha>/aphrollo, and the next CI
// deploy pruned it out from under every one of them.
func TestDoctor_ReportsAGitHookRunningABinaryUnderAVersionedReleaseDir(t *testing.T) {
	in := healthyInstall(t)
	releaseBin := filepath.Join(t.TempDir(), "releases", "20260924-151121-3dcc0ba", "aphrollo")
	writeFakeBinAt(t, releaseBin)
	repointHooks(t, in.GitHooksPath, releaseBin)

	c := check(t, Doctor(in), "hook target stable")
	if c.OK {
		t.Fatalf("a hook naming a path under releases/ must be a finding, got ok: %s", c.Detail)
	}
	for _, want := range []string{filepath.ToSlash(releaseBin), "pre-commit", "aphrollo install"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", c.Detail, want)
		}
	}
}

// The session hooks (settings.json) carry the same risk as the global git
// shims, and this check must see both.
func TestDoctor_ReportsASessionHookRunningABinaryUnderAVersionedReleaseDir(t *testing.T) {
	cfg := t.TempDir()
	releaseBin := filepath.Join(t.TempDir(), "releases", "x", "aphrollo")
	writeFakeBinAt(t, releaseBin)
	if _, err := InitSettings(cfg, releaseBin, false); err != nil {
		t.Fatal(err)
	}
	in := healthyInstall(t)
	in.ConfigDir = cfg
	in.Bin = releaseBin

	c := check(t, Doctor(in), "hook target stable")
	if c.OK {
		t.Fatalf("a session hook naming a path under releases/ must be a finding, got ok: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, filepath.ToSlash(releaseBin)) {
		t.Errorf("detail = %q, want it to name %q", c.Detail, releaseBin)
	}
}

// The healthy install's hooks name an ordinary path, so this check passes —
// pinned by TestDoctor_HealthyInstallPassesEveryCheck too, but named here so
// a break in this specific check is not lost in that check's aggregate loop.
func TestDoctor_HookTargetStablePassesForAnOrdinaryPath(t *testing.T) {
	in := healthyInstall(t)
	if c := check(t, Doctor(in), "hook target stable"); !c.OK {
		t.Fatalf("an ordinary install path must pass, got: %s", c.Detail)
	}
}

// writeFakeBinAt writes an executable stand-in at path, creating its parent
// directories — healthyInstall's own writeFakeBin equivalent, but for a path
// this test builds itself rather than one under a single t.TempDir().
func writeFakeBinAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
}
