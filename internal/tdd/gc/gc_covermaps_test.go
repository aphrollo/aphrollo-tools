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
