package tdd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeTargetDir builds the shape cargo leaves behind: a CACHEDIR.TAG and a
// .rustc_info.json at the top, aged to `idle`.
func makeTargetDir(t *testing.T, path string, idle time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "debug"), 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-idle)
	for _, f := range []string{"CACHEDIR.TAG", ".rustc_info.json", "debug/lib.rlib"} {
		p := filepath.Join(path, filepath.FromSlash(f))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(cands []GCCandidate) map[string]bool {
	out := map[string]bool{}
	for _, c := range cands {
		out[c.Path] = true
	}
	return out
}

func TestStrayTargetDirsProposesAnIdleHandMadeTargetDir(t *testing.T) {
	root := t.TempDir()
	stray := filepath.Join(root, "target-sky")
	resolved := filepath.Join(root, "target")
	makeTargetDir(t, stray, 5*24*time.Hour)
	makeTargetDir(t, resolved, 5*24*time.Hour)

	got := gcStrayTargetDirs([]string{root}, resolved, DefaultGCAge, time.Now())
	if len(got) != 1 || got[0].Path != stray {
		t.Fatalf("candidates = %v, want only %s", paths(got), stray)
	}
	if got[0].Kind != GCKindStrayTarget || got[0].Size == 0 {
		t.Errorf("candidate = %+v", got[0])
	}
}

func TestStrayTargetDirsSpareTheResolvedTargetAndTheStillWarmOne(t *testing.T) {
	root := t.TempDir()
	resolved := filepath.Join(root, "target")
	warm := filepath.Join(root, "target-sky")
	makeTargetDir(t, resolved, 30*24*time.Hour)
	makeTargetDir(t, warm, time.Hour)

	if got := gcStrayTargetDirs([]string{root}, resolved, DefaultGCAge, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %v, want none — one is live, one is warm", paths(got))
	}
}

func TestStrayTargetDirsIgnoresADirectoryThatIsNotACargoTarget(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "crates")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A CACHEDIR.TAG alone is not enough: plenty of caches carry one, and only
	// cargo writes .rustc_info.json beside it.
	tagOnly := filepath.Join(root, "cache")
	if err := os.MkdirAll(tagOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tagOnly, "CACHEDIR.TAG"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := gcStrayTargetDirs([]string{root}, filepath.Join(root, "target"), DefaultGCAge, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %v, want none", paths(got))
	}
}

func TestStrayTargetDirsScansEveryGivenRootAtDepthOneOnly(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	worktree := filepath.Join(base, "lane")
	stray := filepath.Join(worktree, "target-old")
	deep := filepath.Join(repo, "crates", "a", "target-nested")
	makeTargetDir(t, stray, 5*24*time.Hour)
	makeTargetDir(t, deep, 5*24*time.Hour)

	got := gcStrayTargetDirs([]string{repo, worktree}, filepath.Join(repo, "target"), DefaultGCAge, time.Now())
	if !paths(got)[stray] {
		t.Errorf("a stray under a worktree root must be proposed: %v", paths(got))
	}
	if paths(got)[deep] {
		t.Errorf("depth 1 only — a nested dir is somebody's layout: %v", paths(got))
	}
}

// ratchet: test_removed TestStrayTargetSweepTakesNoBuildSlot: renamed to
// TestStrayTargetSweep_TakesABuildSlotOnItsOwnPath, asserting the OPPOSITE
// of the old name's claim — see issue #285, the old assumption was wrong.
//
// A "stray" target is only stray because it does not match this repo's OWN
// resolved target dir — which is exactly the comparison a target-dir
// misresolution gets wrong (issue #285: a repo configuring build.target-dir
// through .cargo/config.toml had its real, LIVE target dir proposed here).
// Even a genuinely stray one can be the live target of some OTHER ad hoc
// `--target-dir` invocation on the box. Interlocked on its OWN path, like
// every other target-scoped category, rather than assumed safe.
func TestStrayTargetSweep_TakesABuildSlotOnItsOwnPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target-sky")
	if got := gcTargetInterlock(t.TempDir(), GCCandidate{Kind: GCKindStrayTarget, Path: path}); got != path {
		t.Errorf("interlock = %q, want the candidate's own path %q", got, path)
	}
}

func TestAllGCScopesIncludesStrayTargets(t *testing.T) {
	if !AllGCScopes().StrayTargets {
		t.Error("the manual sweep must consider stray target dirs")
	}
}
