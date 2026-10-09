package merge

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// fanpyp v1.43.0: the merge checkout linked lane L4's node_modules, and L4's
// react resolved into lane L6. Node dedupes by real path, so the suite loaded
// two Reacts ("Invalid hook call") and the merge was refused as failing tests.
// The fixture is that lane: its fakepkg is a link into a donor lane's copy.

// donorPkgBody is what the donor lane's copy says; the checkout's own install
// (installFakePkg) says something else, so a suite run can tell which one it
// resolved.
const donorPkgBody = "module.exports = 'donor'\n"

// mixedNpmLane is npmPRGateLane (lockfile unchanged) whose installed fakepkg
// resolves into donor, another lane's tree, and returns lane and donor.
func mixedNpmLane(t *testing.T) (lane, donorPkg string) {
	t.Helper()
	lane = npmPRGateLane(t, false)
	donorPkg = filepath.Join(t.TempDir(), "donor-lane", "frontend", "node_modules", "fakepkg")
	if err := os.MkdirAll(donorPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(donorPkg, "index.js"), []byte(donorPkgBody), 0o644); err != nil {
		t.Fatal(err)
	}
	laneLink := filepath.Join(lane, "frontend", "node_modules", "fakepkg")
	if err := os.RemoveAll(laneLink); err != nil {
		t.Fatal(err)
	}
	if err := depinstall.LinkDir(donorPkg, laneLink); err != nil {
		t.Fatalf("a directory link could not be made: %v", err)
	}
	return lane, donorPkg
}

// resolving wraps the npm fake so every suite call records the body of the
// fakepkg its npm root resolved.
func resolving(t *testing.T, seen *[]npmRun, bodies *[]string) SuiteRunner {
	inner := npmFakeRun(t, seen, SuiteResult{Passed: true})
	return func(r Runner, dir string) SuiteResult {
		d := dir
		if r.Dir != "" {
			d = r.Dir
		}
		if filepath.Base(d) == "frontend" && (r.Cmd != "npm" || len(r.Args) != 1 || r.Args[0] != "ci") {
			if b, err := os.ReadFile(filepath.Join(d, "node_modules", "fakepkg", "index.js")); err == nil {
				*bodies = append(*bodies, string(b))
			}
		}
		return inner(r, dir)
	}
}

func installCount(seen []npmRun) int {
	n := 0
	for _, r := range seen {
		if r.isInstall() {
			n++
		}
	}
	return n
}

// The bug: a lane whose package resolves into another lane is not a source of
// dependencies. The merge installs its own, and the suite never resolves the
// donor's copy.
func TestGatePRMerge_NpmRootDoesNotLinkALaneWhosePackageResolvesIntoAnotherLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane, _ := mixedNpmLane(t)

	var seen []npmRun
	var bodies []string
	if err := GatePRMerge(lane, "", resolving(t, &seen, &bodies), io.Discard); err != nil {
		t.Fatalf("the merge must land on its own install: %v", err)
	}
	if n := installCount(seen); n != 1 {
		t.Errorf("ran %d install(s), want 1: the lane's mixed node_modules is not a source", n)
	}
	if len(bodies) == 0 {
		t.Fatal("no suite resolved fakepkg")
	}
	for _, b := range bodies {
		if b == donorPkgBody {
			t.Errorf("the suite resolved fakepkg from the donor lane: two copies of a module")
		}
	}
	requireLaneNodeModulesIntact(t, lane)
}

// The refusal-free path names why it installed, with the package and where it
// really resolved, so the operator can see which lane was the donor.
func TestGatePRMerge_NpmRootSaysWhichPackageEscapedAndWhere(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane, _ := mixedNpmLane(t)

	var log strings.Builder
	var seen []npmRun
	var bodies []string
	if err := GatePRMerge(lane, "", resolving(t, &seen, &bodies), &log); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"not self-contained", "fakepkg", "donor-lane"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

// Two merges with the same lockfile share one install, keyed on the content,
// not on any lane: the second links the first's and installs nothing.
func TestGatePRMerge_NpmRootReusesTheInstallOfTheSameLockfile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane, _ := mixedNpmLane(t)

	var seen []npmRun
	var bodies []string
	for i := range 2 {
		if err := GatePRMerge(lane, "", resolving(t, &seen, &bodies), io.Discard); err != nil {
			t.Fatalf("merge %d: %v", i+1, err)
		}
	}
	if n := installCount(seen); n != 1 {
		t.Errorf("ran %d installs for two merges of one lockfile, want 1", n)
	}
	if len(bodies) < 2 {
		t.Fatalf("suite resolved fakepkg %d times, want once per merge", len(bodies))
	}
	for _, b := range bodies {
		if b != "module.exports = 1\n" {
			t.Errorf("a suite resolved %q, want the checkout's own install", b)
		}
	}
}

// A moved lockfile is another install: the cache is keyed on the bytes.
func TestGatePRMerge_NpmRootInstallsAgainWhenTheLockfileChanges(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane, _ := mixedNpmLane(t)

	var seen []npmRun
	var bodies []string
	if err := GatePRMerge(lane, "", resolving(t, &seen, &bodies), io.Discard); err != nil {
		t.Fatal(err)
	}
	// trunk bumps the lockfile; the lane still holds the mixed install
	trunk := TrunkBranch(lane)
	gitDo(t, lane, "checkout", "-q", trunk)
	write(t, lane, "frontend/package-lock.json", npmLockBumped)
	gitDo(t, lane, "add", ".")
	gitDo(t, lane, "commit", "-qm", "trunk bumps the lockfile again")
	gitDo(t, lane, "checkout", "-q", "lane")
	if err := GatePRMerge(lane, "", resolving(t, &seen, &bodies), io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := installCount(seen); n != 2 {
		t.Errorf("ran %d installs across two lockfiles, want 2", n)
	}
}
