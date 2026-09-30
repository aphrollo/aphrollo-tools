package lawgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lane build stages on the gate's disk scratch dir, the one the scratch
// sweep reads, never in the OS temp dir (issue #1005).
func TestLaneBuildDir_StagesUnderTheGoScratchDir(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "gotmp")

	dir, err := laneBuildDir(scratch)
	if err != nil {
		t.Fatalf("laneBuildDir: %v", err)
	}
	if got := filepath.Dir(dir); got != scratch {
		t.Errorf("lane build dir %q sits in %q, want directly under %q", dir, got, scratch)
	}
	if !strings.HasPrefix(filepath.Base(dir), "aphrollo-lane-") {
		t.Errorf("lane build dir %q is not named aphrollo-lane-*", dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("lane build dir %q was not created: %v", dir, err)
	}
}

// No resolvable scratch dir: the build still runs, in the OS temp dir.
func TestLaneBuildDir_EmptyScratchFallsBackToTheOSTempDir(t *testing.T) {
	dir, err := laneBuildDir("")
	if err != nil {
		t.Fatalf("laneBuildDir: %v", err)
	}
	defer os.RemoveAll(dir)
	if got, want := filepath.Dir(dir), filepath.Clean(os.TempDir()); got != want {
		t.Errorf("lane build dir %q sits in %q, want the OS temp dir %q", dir, got, want)
	}
}

// A checkout whose primary resolves gets its build scratch beside the
// worktrees, the directory GoTmpRootDir names for the sweep.
func TestLaneFixtureBuild_ScratchIsTheGateGoTmpRoot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, repo, "init", "-q")
	want := GoTmpRootDir(repo)
	if want == "" {
		t.Fatal("a git checkout resolves no go-scratch dir")
	}
	dir, err := laneBuildDir(want)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != want {
		t.Errorf("lane build dir %q not under %q", dir, want)
	}
}
