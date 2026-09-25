package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Live incident, 2026-09-25: `aphrollo install` run with no --bin took its
// default from os.Executable(), which resolves EVERY symlink hop. On this
// box that lands on /opt/aphrollo-cli/releases/<ts>-<sha>/aphrollo — the
// directory deploy-prod.sh promotes through a `current` symlink and PRUNES a
// few releases later. Every managed shim then named a path the very next
// deploy removed, and git/cargo silently ran UNGATED ("gate: ... is missing
// — running git UNGATED") with nothing else saying so.
//
// symlinkChain builds the fixture: stable -> current -> releases/x/aphrollo,
// mirroring deploy-prod.sh's own layout (OPT_BASE/current is a directory
// symlink to OPT_BASE/releases/<release>, and /usr/local/bin/aphrollo is a
// file symlink into it).
func symlinkChain(t *testing.T) (dir, stable, releaseX string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no symlink chain to build on Windows — the self-install path never uses one") // skip-ok: Windows self-install places the binary directly under the user's own bin dir (selfinstall.go's binExtForOS/resolveBinPath), never through a symlink chain, and creating one needs an elevated token on stock Windows
	}
	dir = t.TempDir()
	releaseX = filepath.Join(dir, "releases", "x")
	if err := os.MkdirAll(releaseX, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(releaseX, "aphrollo")
	if err := os.WriteFile(bin, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "current")
	if err := os.Symlink(releaseX, current); err != nil {
		t.Fatal(err)
	}
	stable = filepath.Join(dir, "stable")
	if err := os.Symlink(filepath.Join(current, "aphrollo"), stable); err != nil {
		t.Fatal(err)
	}
	return dir, stable, releaseX
}

// fakeRunningAs points execPathFn (what os.Executable() would resolve to)
// and os.Args[0] (what the operator actually typed / the shell resolved
// through PATH) at the fixture, restoring both after the test.
func fakeRunningAs(t *testing.T, resolvedExe, argv0 string) {
	t.Helper()
	origExec, origArgs := execPathFn, os.Args
	execPathFn = func() (string, error) { return resolvedExe, nil }
	os.Args = []string{argv0}
	t.Cleanup(func() { execPathFn = origExec; os.Args = origArgs })
}

// TestDefaultBinPath_PrefersTheStableSymlinkOverAResolvedVersionedRelease is
// the RED this defect needed: defaultBinPath must answer with the stable
// entry point (what os.Args[0] resolves to on PATH, one hop, unfollowed),
// never the versioned release leaf os.Executable() actually resolves to.
func TestDefaultBinPath_PrefersTheStableSymlinkOverAResolvedVersionedRelease(t *testing.T) {
	_, stable, releaseX := symlinkChain(t)
	resolvedExe := filepath.Join(releaseX, "aphrollo")
	fakeRunningAs(t, resolvedExe, stable)

	got := defaultBinPath()
	if got != stable {
		t.Fatalf("defaultBinPath() = %q, want the stable path %q (never the versioned release leaf %q)", got, stable, resolvedExe)
	}
}

// TestDefaultBinPath_StableSymlinkSurvivesADeploy proves the point of the
// fix: after a "deploy" repoints `current` at a new release and prunes the
// old one, the stable path defaultBinPath chose still resolves — exactly the
// property the resolved release-leaf path does NOT have.
func TestDefaultBinPath_StableSymlinkSurvivesADeploy(t *testing.T) {
	dir, stable, releaseX := symlinkChain(t)
	resolvedExe := filepath.Join(releaseX, "aphrollo")
	fakeRunningAs(t, resolvedExe, stable)

	got := defaultBinPath()
	if got != stable {
		t.Fatalf("defaultBinPath() = %q, want %q", got, stable)
	}

	// "Deploy": current -> releases/y, then releases/x is pruned, matching
	// deploy-prod.sh's atomic-swap-then-prune sequence.
	releaseY := filepath.Join(dir, "releases", "y")
	if err := os.MkdirAll(releaseY, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseY, "aphrollo"), []byte("APHROLLO2"), 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "current")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(releaseY, current); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(releaseX); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(got); err != nil {
		t.Fatalf("the path defaultBinPath chose must still resolve after the deploy: %v", err)
	}
}

// A --bin whose ONLY reachable spelling is the versioned-release path itself
// (os.Args[0] gives no stable alternative, e.g. a direct invocation of the
// release binary) falls back to the `current` sibling deploy-prod.sh's own
// convention promotes a release through, rather than writing the doomed leaf.
func TestDefaultBinPath_FallsBackToCurrentSiblingWithNoStableArgv(t *testing.T) {
	dir, _, releaseX := symlinkChain(t)
	resolvedExe := filepath.Join(releaseX, "aphrollo")
	// os.Args[0] names the release leaf directly, exactly as it would if the
	// operator (or a script) invoked the binary by its resolved path rather
	// than through any symlink.
	fakeRunningAs(t, resolvedExe, resolvedExe)

	got := defaultBinPath()
	want := filepath.Join(dir, "current", "aphrollo")
	if got != want {
		t.Fatalf("defaultBinPath() = %q, want the `current` sibling %q, never the versioned leaf %q", got, want, resolvedExe)
	}
}

// A binary that is not under any versioned-release directory at all (the
// common case: a plain install, a self-installed Windows binary, or the test
// binary running this suite) is returned unchanged — there is nothing to fix.
func TestDefaultBinPath_LeavesAnOrdinaryPathUnchanged(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "aphrollo")
	if err := os.WriteFile(bin, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeRunningAs(t, bin, bin)

	if got := defaultBinPath(); got != bin {
		t.Fatalf("defaultBinPath() = %q, want %q unchanged", got, bin)
	}
}
