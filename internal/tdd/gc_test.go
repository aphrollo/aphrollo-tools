package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func mkFile(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	tddtest.MkFile(t, path, content, age)
}

// TestGCIncremental_OnlyIdleCachesAndNeverTheArtifacts pins category (a):
// an incremental cache nobody has touched for longer than the age is
// reclaimable (it costs one recompile of that crate), a fresh one is not,
// and the artifact directories a rebuild would otherwise have to redo from
// scratch — deps/, build/, .fingerprint/ — are never candidates at all.
func TestGCIncremental_OnlyIdleCachesAndNeverTheArtifacts(t *testing.T) {
	target := t.TempDir()
	mkFile(t, filepath.Join(target, "debug", "incremental", "stale-1a2b", "s-x", "dep-graph.bin"), "xxxx", 10*24*time.Hour)
	mkFile(t, filepath.Join(target, "debug", "incremental", "fresh-3c4d", "s-y", "dep-graph.bin"), "yyyy", 0)
	mkFile(t, filepath.Join(target, "debug", "deps", "libfoo.rlib"), "zzzz", 30*24*time.Hour)
	mkFile(t, filepath.Join(target, "debug", "build", "foo-1234", "out"), "zzzz", 30*24*time.Hour)
	mkFile(t, filepath.Join(target, "debug", ".fingerprint", "foo-1234", "lib-foo"), "zzzz", 30*24*time.Hour)

	got := gcIncremental(target, 3*24*time.Hour, time.Now())
	if len(got) != 1 {
		t.Fatalf("want exactly the one idle incremental cache, got %d: %+v", len(got), got)
	}
	if filepath.Base(got[0].Path) != "stale-1a2b" {
		t.Fatalf("candidate = %q, want the stale incremental cache", got[0].Path)
	}
	if got[0].Size != 4 {
		t.Errorf("size = %d bytes, want the 4 bytes actually on disk", got[0].Size)
	}
	if !strings.Contains(got[0].Reason, "incremental") {
		t.Errorf("reason = %q, want it to say what the directory is", got[0].Reason)
	}
}

// TestGCIncremental_AgeIsTheNEWESTFileInTheTree pins the rule that decides
// idleness: a cache with one recently-written file is IN USE even if most
// of it is old. Judging by the directory's own mtime (which does not change
// when a nested file is rewritten) would delete a cache being used right
// now, costing a full recompile mid-session.
func TestGCIncremental_AgeIsTheNEWESTFileInTheTree(t *testing.T) {
	target := t.TempDir()
	cache := filepath.Join(target, "debug", "incremental", "mixed-9f8e")
	mkFile(t, filepath.Join(cache, "s-old", "dep-graph.bin"), "old", 30*24*time.Hour)
	mkFile(t, filepath.Join(cache, "s-new", "dep-graph.bin"), "new", time.Hour)

	if got := gcIncremental(target, 3*24*time.Hour, time.Now()); len(got) != 0 {
		t.Fatalf("a cache touched an hour ago must never be a candidate, got: %+v", got)
	}
}

// TestGCStaleGateDirs_OnlyWhenTheRecordedRootIsGone pins category (b): the
// gate's per-repo fail-first worktree is keyed by a hash,
// so nothing about the directory says which repo it belongs to — the
// origin.txt written at creation does. A dir whose origin still exists is
// live; a dir with NO origin.txt is unknown and is left alone rather than
// guessed at.
func TestGCStaleGateDirs_OnlyWhenTheRecordedRootIsGone(t *testing.T) {
	base := t.TempDir()
	liveRepo := t.TempDir()

	live := filepath.Join(base, "failfirst-wt", "aaaa1111")
	mkFile(t, filepath.Join(live, "origin.txt"), liveRepo, 0)
	mkFile(t, filepath.Join(live, "debug", "libfoo.rlib"), "aaaa", 0)

	dead := filepath.Join(base, "failfirst-wt", "bbbb2222")
	mkFile(t, filepath.Join(dead, "origin.txt"), filepath.Join(base, "no-such-repo"), 0)
	mkFile(t, filepath.Join(dead, "debug", "libfoo.rlib"), "bbbb", 0)

	deadWT := filepath.Join(base, "failfirst-wt", "cccc3333")
	mkFile(t, filepath.Join(deadWT, "origin.txt"), filepath.Join(base, "also-gone"), 0)
	mkFile(t, filepath.Join(deadWT, "src", "lib.rs"), "cc", 0)

	unknown := filepath.Join(base, "failfirst-wt", "dddd4444")
	mkFile(t, filepath.Join(unknown, "debug", "libfoo.rlib"), "dddd", 0)

	got := gcStaleGateDirs(base)
	paths := map[string]bool{}
	for _, c := range got {
		paths[c.Path] = true
	}
	if !paths[dead] || !paths[deadWT] {
		t.Fatalf("both dirs whose recorded root is gone must be candidates, got: %+v", got)
	}
	if paths[live] {
		t.Error("a gate dir whose repo still exists must never be a candidate")
	}
	if paths[unknown] {
		t.Error("a gate dir with no origin.txt is unknown — it must be left alone, not guessed at")
	}
}

// TestGCOrphanWorktreeDirs_OnlyBuildOnlyDirsBesideRegisteredOnes pins
// category (c): under the directory where a repo's external worktrees live,
// a leftover holding nothing but target/ is an orphan build dir (git dropped
// the worktree, the target survived). A REGISTERED worktree is never
// touched, and neither is a directory that still holds source.
func TestGCOrphanWorktreeDirs_OnlyBuildOnlyDirsBesideRegisteredOnes(t *testing.T) {
	repo := makeCargoRepo(t)
	wtParent := t.TempDir()

	live := filepath.Join(wtParent, "lane-live")
	gitDo(t, repo, "worktree", "add", "-b", "lane/live", live)
	mkFile(t, filepath.Join(live, "target", "debug", "x.rlib"), "live", 0)

	orphan := filepath.Join(wtParent, "lane-gone")
	mkFile(t, filepath.Join(orphan, "target", "debug", "x.rlib"), "orph", 0)
	mkFile(t, filepath.Join(orphan, ".DS_Store"), "d", 0)

	withSource := filepath.Join(wtParent, "lane-hand-made")
	mkFile(t, filepath.Join(withSource, "target", "debug", "x.rlib"), "hand", 0)
	mkFile(t, filepath.Join(withSource, "Cargo.toml"), "[package]\n", 0)

	got := gcOrphanWorktreeDirs(repo)
	if len(got) != 1 {
		t.Fatalf("want exactly the one orphan build dir, got %d: %+v", len(got), got)
	}
	if got[0].Path != orphan {
		t.Fatalf("candidate = %q, want the orphan %q", got[0].Path, orphan)
	}
}

// TestApplyGC_DeletesAndRefusesProtectedPaths pins what --apply is allowed
// to do: remove exactly the candidates it was handed, reporting the bytes
// freed — and refuse a path naming an artifact directory even if one is
// somehow handed to it, since that is the class the whole feature is
// written to never touch.
func TestApplyGC_DeletesAndRefusesProtectedPaths(t *testing.T) {
	base := t.TempDir()
	doomed := filepath.Join(base, "incremental", "stale")
	mkFile(t, filepath.Join(doomed, "f"), "12345", 0)
	protected := filepath.Join(base, "debug", "deps")
	mkFile(t, filepath.Join(protected, "libfoo.rlib"), "123", 0)

	freed, refused := ApplyGC([]GCCandidate{
		{Path: doomed, Size: 5, Reason: "incremental cache, idle 9d"},
		{Path: protected, Size: 3, Reason: "bogus"},
	})
	if freed != 5 {
		t.Errorf("freed = %d, want the 5 bytes of the one legitimate candidate", freed)
	}
	if _, err := os.Stat(doomed); !os.IsNotExist(err) {
		t.Error("the candidate must be gone")
	}
	if _, err := os.Stat(protected); err != nil {
		t.Error("deps/ must survive — it is never reclaimable")
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "deps") {
		t.Errorf("the refusal must name the path it refused, got %+v", refused)
	}
}

// TestRenderGC_DryRunTableNamesPathSizeReasonAndTheApplyCommand pins the
// output contract: the operator sees WHAT would go, HOW BIG it is, WHY, and
// the exact command that does it. A dry run must never read as if it
// deleted something.
func TestRenderGC_DryRunTableNamesPathSizeReasonAndTheApplyCommand(t *testing.T) {
	out := RenderGC([]GCCandidate{
		{Path: `D:\Projects\borld\target\debug\incremental\borld-1a2b`, Size: 12_884_901_888, Reason: "incremental cache, idle 9d"},
	}, false, 0)
	for _, want := range []string{"borld-1a2b", "12.0 GB", "idle 9d", "aphrollo gate gc --apply"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "freed") {
		t.Errorf("a dry run must not claim to have freed anything:\n%s", out)
	}
}

// TestRenderGC_EmptyScanSaysSoOnce pins the quiet case: nothing reclaimable
// is one line, not an empty table.
func TestRenderGC_EmptyScanSaysSoOnce(t *testing.T) {
	out := RenderGC(nil, false, 0)
	if !strings.Contains(out, "nothing") || strings.Contains(out, "--apply") {
		t.Fatalf("empty scan output = %q, want a single 'nothing reclaimable' line", out)
	}
}

// TestParseGCAge_DaysAndDurations pins the --older-than grammar: days is
// the unit a build cache is actually reasoned about in, and Go's own
// duration syntax does not have one.
func TestParseGCAge_DaysAndDurations(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"3d", 72 * time.Hour, true},
		{"14d", 14 * 24 * time.Hour, true},
		{"12h", 12 * time.Hour, true},
		{"90m", 90 * time.Minute, true},
		{"", 0, false},
		{"soon", 0, false},
		{"-3d", 0, false},
	}
	for _, c := range cases {
		got, err := ParseGCAge(c.in)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("ParseGCAge(%q) = (%s, %v), want (%s, ok=%v)", c.in, got, err, c.want, c.ok)
		}
	}
}

// TestScanGC_ReportsAbsolutePaths pins that a candidate is named by a path
// that means the same thing wherever it is read. `aphrollo gate gc` defaults
// to --repo ".", and a table of "target\debug\incremental\..." lines is
// ambiguous the moment it is pasted anywhere, or acted on from another
// directory.
func TestScanGC_ReportsAbsolutePaths(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	mkFile(t, filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b", "f"), "x", 30*24*time.Hour)
	t.Chdir(repo)

	got := ScanGC(".", 3*24*time.Hour, GCScope{Incremental: true})
	if len(got) != 1 {
		t.Fatalf("want the one stale cache, got %+v", got)
	}
	if !filepath.IsAbs(got[0].Path) {
		t.Fatalf("candidate path = %q, want an absolute path", got[0].Path)
	}
}

// TestScanGC_ListsEachDirectoryOnce pins issue #565's third item: an
// incremental unit dir belongs to two categories at once — the incremental
// sweep proposes it as "incremental cache" and the deps tiers propose the
// same directory as "<crate> unit dir" — so the table printed every one of
// them twice, same path and same size, and a sweep counted its bytes twice
// over (the second RemoveAll of a path already gone reports no error).
func TestScanGC_ListsEachDirectoryOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CARGO_TARGET_DIR", "")
	repo := t.TempDir()
	cache := filepath.Join(repo, "target", "debug", "incremental", "server-1a2b3c4d5e6f7a8b")
	mkFile(t, filepath.Join(cache, "f"), "x", 30*24*time.Hour)
	// A profile counts as one only when it has a deps/ dir, and this is the
	// artifact tier's own candidate — one more path that must appear once.
	mkFile(t, filepath.Join(repo, "target", "debug", "deps", "libserver-0123456789abcdef.rlib"), "x", 30*24*time.Hour)

	times := map[string]int{}
	for _, c := range ScanGC(repo, 3*24*time.Hour, GCScope{Incremental: true, DepsArtifacts: true}) {
		times[c.Path]++
	}
	if times[cache] != 1 {
		t.Errorf("the incremental cache is listed %d times, want once", times[cache])
	}
	for path, n := range times {
		if n != 1 {
			t.Errorf("%s is listed %d times, want once", path, n)
		}
	}
}
