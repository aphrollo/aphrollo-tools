package gc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func agedCoverFile(t *testing.T, dir, name string, age time.Duration, now time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := now.Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

// A coverage map nothing has read for the age bar is swept, one read since is
// kept, and what is not a map is never proposed.
func TestGCCoverMapFiles_ProposesOnlyMapsNothingHasReadForTheAgeBar(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := agedCoverFile(t, dir, "internal__p-aaaa.json", 31*24*time.Hour, now)
	agedCoverFile(t, dir, "internal__q-bbbb.json", 29*24*time.Hour, now)
	partial := agedCoverFile(t, dir, ".testmap-123", 25*time.Hour, now)
	agedCoverFile(t, dir, ".testmap-456", 2*time.Hour, now) // a save that may still be running
	agedCoverFile(t, dir, "notes.txt", 90*24*time.Hour, now)
	if err := os.Mkdir(filepath.Join(dir, "old.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	got := gcCoverMapFiles(dir, 30*24*time.Hour, now)

	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
		if c.Size == 0 {
			t.Errorf("candidate %s has no size", c.Path)
		}
	}
	want := []string{partial, stale}
	slices.Sort(want)
	if !slices.Equal(paths, want) {
		t.Errorf("candidates = %v, want %v", paths, want)
	}
}

func TestGCCoverMapFiles_ADirectoryThatIsNotThereProposesNothing(t *testing.T) {
	if got := gcCoverMapFiles(filepath.Join(t.TempDir(), "absent"), time.Hour, time.Now()); len(got) != 0 {
		t.Errorf("candidates = %v, want none", got)
	}
}

func TestGCCoverMaps_OutsideARepositoryProposesNothing(t *testing.T) {
	if got := gcCoverMaps(t.TempDir(), time.Now()); len(got) != 0 {
		t.Errorf("candidates = %v, want none", got)
	}
}

// A map is proposed from the very age bar on, and a half-written file from a
// day on: one moment short of either is kept.
func TestGCCoverMapFiles_TheAgeBarsAreInclusive(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	atBar := agedCoverFile(t, dir, "a-1.json", 30*24*time.Hour, now)
	agedCoverFile(t, dir, "b-2.json", 30*24*time.Hour-time.Minute, now)
	partial := agedCoverFile(t, dir, ".testmap-1", 24*time.Hour, now)
	agedCoverFile(t, dir, ".testmap-2", 24*time.Hour-time.Minute, now)

	var paths []string
	for _, c := range gcCoverMapFiles(dir, 30*24*time.Hour, now) {
		paths = append(paths, c.Path)
	}
	want := []string{atBar, partial}
	slices.Sort(want)
	if !slices.Equal(paths, want) {
		t.Errorf("candidates = %v, want %v", paths, want)
	}
}

// The scan finds a repository's stale maps in its shared git directory, for the
// manual command and for the hook alike.
func TestScanGC_ProposesAStaleCoverMapOfTheRepository(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	dir := CoverCacheDir(repo)
	if dir == "" {
		t.Fatal("no cover cache directory for a git repository")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stale := agedCoverFile(t, dir, "internal__p-aaaa.json", CoverCacheMaxAge+time.Hour, now)
	fresh := agedCoverFile(t, dir, "internal__q-bbbb.json", time.Hour, now)

	var found []string
	for _, c := range ScanGC(repo, DefaultGCAge, GCScope{GateDirs: true}) {
		found = append(found, c.Path)
	}
	if !slices.Contains(found, stale) || slices.Contains(found, fresh) {
		t.Errorf("scan proposed %v, want the stale map %s and not the fresh %s", found, stale, fresh)
	}
	if got := gcCoverMaps(repo, now); len(got) != 1 || got[0].Path != stale {
		t.Errorf("gcCoverMaps = %+v, want only the stale map", got)
	}
}
