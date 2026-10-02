package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// staleDir lays out a bin dir holding the named files, each a plain-bytes
// binary.
func staleDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func notInUse(t *testing.T) {
	t.Helper()
	prev := copyInUseFn
	copyInUseFn = func(string) bool { return false }
	t.Cleanup(func() { copyInUseFn = prev })
}

// The name says when the copy was retired, and the order of those names is the
// order the copies are kept in. A clock that stepped back, or two swaps in one
// second, must never give a new copy a name that sorts among the old ones, or
// the retention would delete the newest copy.
func TestNextStalePath_AlwaysSortsAfterEveryCopyThatIsAlreadyThere(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cases := []struct {
		name     string
		existing []string
		want     string
	}{
		{"no copies yet", nil, "aphrollo.stale-1700000000.exe"},
		{"an older copy", []string{"aphrollo.stale-1699999999.exe"}, "aphrollo.stale-1700000000.exe"},
		{"a copy retired this very second", []string{"aphrollo.stale-1700000000.exe"}, "aphrollo.stale-1700000001.exe"},
		{"a copy dated in the future", []string{"aphrollo.stale-1800000000.exe"}, "aphrollo.stale-1800000001.exe"},
		{"a copy with no number is no later than any", []string{"aphrollo.stale-old.exe"}, "aphrollo.stale-1700000000.exe"},
		{"another binary's copies are not counted", []string{"other.stale-1800000000.exe"}, "aphrollo.stale-1700000000.exe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := staleDir(t, c.existing...)
			got := nextStalePath(filepath.Join(dir, "aphrollo.exe"), now)
			if want := filepath.Join(dir, c.want); got != want {
				t.Fatalf("nextStalePath = %q, want %q", got, want)
			}
		})
	}
}

func TestPruneKeptBinaries_KeepsTheNewestTwoCopiesByNumberNotByName(t *testing.T) {
	notInUse(t)
	// Lexically "9" sorts after "100"; by the number it is the oldest.
	dir := staleDir(t, "aphrollo.exe", "aphrollo.stale-9.exe", "aphrollo.stale-10.exe", "aphrollo.stale-100.exe")

	removed, held := pruneKeptBinaries(dir, "aphrollo.exe")

	if removed != 1 || held != 0 {
		t.Fatalf("pruneKeptBinaries = (%d removed, %d held), want (1, 0)", removed, held)
	}
	want := []string{"aphrollo.exe", "aphrollo.stale-10.exe", "aphrollo.stale-100.exe"}
	if got := listDir(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("dir = %v, want %v", got, want)
	}
}

func TestPruneKeptBinaries_ReclaimsACopyWithNoNumberFirst(t *testing.T) {
	notInUse(t)
	dir := staleDir(t, "aphrollo.stale-old.exe", "aphrollo.stale-1.exe", "aphrollo.stale-2.exe")

	removed, _ := pruneKeptBinaries(dir, "aphrollo.exe")

	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	want := []string{"aphrollo.stale-1.exe", "aphrollo.stale-2.exe"}
	if got := listDir(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("dir = %v, want %v", got, want)
	}
}

func TestPruneKeptBinaries_NeverDeletesACopyStillInUse(t *testing.T) {
	dir := staleDir(t, "aphrollo.stale-1.exe", "aphrollo.stale-2.exe", "aphrollo.stale-3.exe", "aphrollo.stale-4.exe")
	inUse := filepath.Join(dir, "aphrollo.stale-1.exe")
	prev := copyInUseFn
	copyInUseFn = func(path string) bool { return path == inUse }
	t.Cleanup(func() { copyInUseFn = prev })

	removed, held := pruneKeptBinaries(dir, "aphrollo.exe")

	if removed != 1 || held != 1 {
		t.Fatalf("pruneKeptBinaries = (%d removed, %d held), want (1, 1)", removed, held)
	}
	want := []string{"aphrollo.stale-1.exe", "aphrollo.stale-3.exe", "aphrollo.stale-4.exe"}
	if got := listDir(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("dir = %v, want the in-use copy and the newest two", got)
	}
}

// A copy the OS will not let go of is reported, never an error.
func TestPruneKeptBinaries_CountsACopyItCannotRemoveAsHeld(t *testing.T) {
	notInUse(t)
	dir := staleDir(t, "aphrollo.stale-3.exe", "aphrollo.stale-4.exe")
	locked := filepath.Join(dir, "aphrollo.stale-1.exe")
	if err := os.MkdirAll(filepath.Join(locked, "held"), 0o755); err != nil { // a non-empty directory cannot be removed
		t.Fatal(err)
	}

	removed, held := pruneKeptBinaries(dir, "aphrollo.exe")

	if removed != 0 || held != 1 {
		t.Fatalf("pruneKeptBinaries = (%d removed, %d held), want (0, 1)", removed, held)
	}
}

// Only the stale copies of THIS binary are ever candidates for deletion.
func TestPruneKeptBinaries_LeavesEverythingThatIsNotAStaleCopyAlone(t *testing.T) {
	notInUse(t)
	dir := staleDir(t, "aphrollo.exe", "aphrollo.new.exe", "aphrollo.installs.json", "other.stale-1.exe",
		"aphrollo.stale-1.exe", "aphrollo.stale-2.exe", "aphrollo.stale-3.exe")

	pruneKeptBinaries(dir, "aphrollo.exe")

	want := []string{"aphrollo.exe", "aphrollo.installs.json", "aphrollo.new.exe", "aphrollo.stale-2.exe", "aphrollo.stale-3.exe", "other.stale-1.exe"}
	if got := listDir(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("dir = %v, want %v", got, want)
	}
}

func TestCopyInUse_AFileThatOpensForWritingIsNotInUse(t *testing.T) {
	dir := staleDir(t, "aphrollo.stale-1.exe")

	if copyInUse(filepath.Join(dir, "aphrollo.stale-1.exe")) {
		t.Fatal("copyInUse = true for a file nothing holds")
	}
}

// What cannot be opened for writing is treated as in use: a copy that may be
// running is never deleted on a guess.
func TestCopyInUse_AFileThatCannotBeOpenedIsTreatedAsInUse(t *testing.T) {
	dir := t.TempDir()

	if !copyInUse(filepath.Join(dir, "aphrollo.stale-1.exe")) {
		t.Fatal("copyInUse = false for a path that cannot be opened")
	}
}
