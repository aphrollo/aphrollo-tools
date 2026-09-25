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

// defaultBinPath must absolutize a RELATIVE os.Executable() answer before
// comparing it against the candidates lookPathAlias/currentSiblingAlias
// build (which are always absolute themselves): skip that step and the
// equality check the whole stable-alias search rests on never matches, even
// for a genuinely correct alias, so the function silently falls back to
// returning the bare relative path it was given.
func TestDefaultBinPath_AbsolutizesARelativeExecutablePath(t *testing.T) {
	dir, stable, releaseX := symlinkChain(t)
	t.Chdir(dir)
	relExe, err := filepath.Rel(dir, filepath.Join(releaseX, "aphrollo"))
	if err != nil {
		t.Fatal(err)
	}
	// os.Args[0] needs a path separator for exec.LookPath to check it
	// directly rather than searching $PATH for a bare command name.
	fakeRunningAs(t, relExe, "./stable")

	if got := defaultBinPath(); got != stable {
		t.Fatalf("defaultBinPath() = %q, want the absolute stable path %q for a relative os.Executable() answer %q", got, stable, relExe)
	}
}

// defaultBinPath must resolve exe's OWN symlinks before judging whether it
// sits under a versioned-release directory: os.Executable() always returns a
// fully resolved path in practice, but the check exists precisely so a path
// that still names a symlink (here, the `current` hop) is followed through
// to the release leaf it actually names before that judgment is made —
// skipping it makes a path through `current` read as "not under releases"
// and returned untouched, rather than resolved on to the stable alias.
func TestDefaultBinPath_ResolvesSymlinksInTheExecutablePathBeforeJudgingIt(t *testing.T) {
	dir, stable, _ := symlinkChain(t)
	unresolved := filepath.Join(dir, "current", "aphrollo")
	fakeRunningAs(t, unresolved, stable)

	if got := defaultBinPath(); got != stable {
		t.Fatalf("defaultBinPath() = %q, want %q for an os.Executable() answer that still names the `current` symlink", got, stable)
	}
}

// rawExecutablePath must absolutize a RELATIVE os.Executable() answer too:
// `aphrollo update`'s writability check (#816) joins this path's own Dir()
// straight into InstallWritable, and a relative Dir() resolves against
// whatever the CURRENT process happens to be running from rather than the
// install directory the operator actually means.
func TestRawExecutablePath_AbsolutizesARelativePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	origExec := execPathFn
	execPathFn = func() (string, error) { return "aphrollo", nil }
	t.Cleanup(func() { execPathFn = origExec })

	want := filepath.Join(dir, "aphrollo")
	if got := rawExecutablePath(); got != want {
		t.Fatalf("rawExecutablePath() = %q, want the absolute path %q for a relative os.Executable() answer", got, want)
	}
}

// currentSiblingAlias's "releases" component needs a <version> AND a <name>
// after it — two components, not one — or there is no current/<name> shape
// to build at all. "releases/aphrollo" (the binary sitting directly under
// releases/, no version directory) has only one, so this must find no alias
// even though a stray `current` happens to sit right beside `releases` and
// resolve to the very same binary: with no <name> left to preserve, dropping
// straight to "current" is not a valid deploy-convention alias, and
// defaultBinPath must fall back to the unresolved path rather than accept
// the coincidence.
func TestDefaultBinPath_NoCurrentSiblingWithNoVersionDirectory(t *testing.T) {
	dir := t.TempDir()
	releaseDir := filepath.Join(dir, "releases")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(releaseDir, "aphrollo")
	if err := os.WriteFile(bin, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A `current` that happens to resolve to the exact same binary, so the
	// only thing standing between a wrong match and the correct "no alias"
	// answer is the two-components-after-releases check itself.
	if err := os.Symlink(bin, filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	fakeRunningAs(t, bin, bin)

	if got := defaultBinPath(); got != bin {
		t.Fatalf("defaultBinPath() = %q, want the unresolved path %q back (no version directory to build a `current` sibling from)", got, bin)
	}
}
