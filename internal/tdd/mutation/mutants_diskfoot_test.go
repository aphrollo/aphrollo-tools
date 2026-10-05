package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctordiskFixture lays out a repo beside its lane worktrees, a Go build cache
// and a temp dir, each holding the given number of bytes, and points the
// doctor at them.
func doctordiskFixture(t *testing.T, cacheBytes, laneBytes, tempBytes int) (repo string) {
	t.Helper()
	spaces := t.TempDir()
	repo = filepath.Join(spaces, "proj")
	cache := filepath.Join(t.TempDir(), "go-build")
	temp := t.TempDir()
	for path, n := range map[string]int{
		filepath.Join(cache, "0a", "aaaa-d"):                         cacheBytes,
		filepath.Join(spaces, ".worktrees", "proj", "lane", "f.txt"): laneBytes,
		filepath.Join(temp, "scratch", "f.bin"):                      tempBytes,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temp)
	}
	prev := doctorGoCacheDirFn
	doctorGoCacheDirFn = func() string { return cache }
	t.Cleanup(func() { doctorGoCacheDirFn = prev })
	return repo
}

func TestDoctorDiskSpace_AWarningNamesTheGoCacheTheLanesAndTempBiggestFirst(t *testing.T) {
	withFreeSpace(t, 12)
	repo := doctordiskFixture(t, 3<<20, 7<<20, 1<<20)

	c := doctorDiskSpace(DoctorInput{Repo: repo})

	if !c.Warn {
		t.Fatalf("check = %+v, want a warning at 12 GB free", c)
	}
	lanes, cache, temp := strings.Index(c.Detail, "lane worktrees 7.0 MB"), strings.Index(c.Detail, "go build cache 3.0 MB"), strings.Index(c.Detail, "temp 1.0 MB")
	if lanes < 0 || cache < 0 || temp < 0 {
		t.Fatalf("detail = %q, want all three sizes named", c.Detail)
	}
	if lanes >= cache || cache >= temp {
		t.Errorf("detail = %q, want them biggest first: lanes, cache, temp", c.Detail)
	}
}

func TestDoctorDiskSpace_ACleanReportWalksNothing(t *testing.T) {
	withFreeSpace(t, 200)
	repo := doctordiskFixture(t, 3<<20, 7<<20, 1<<20)

	c := doctorDiskSpace(DoctorInput{Repo: repo})

	if !c.OK || c.Warn {
		t.Fatalf("check = %+v, want a clean report at 200 GB free", c)
	}
	if strings.Contains(c.Detail, "lane worktrees") {
		t.Errorf("detail = %q: a drive with room needs no sizes, and walking a 200 GB cache for them would hold the doctor up", c.Detail)
	}
}

func TestDoctorFootprintDirs_ALaneChecksOutItsSiblingsDirectory(t *testing.T) {
	spaces := t.TempDir()
	lane := filepath.Join(spaces, ".worktrees", "proj", "lane-a")

	if got := laneWorktreesDir(lane); got != filepath.Join(spaces, ".worktrees", "proj") {
		t.Errorf("laneWorktreesDir(lane) = %q, want the directory holding every lane of proj", got)
	}
	if got := laneWorktreesDir(filepath.Join(spaces, "proj")); got != filepath.Join(spaces, ".worktrees", "proj") {
		t.Errorf("laneWorktreesDir(primary) = %q", got)
	}
}

func TestDoctorDiskSpace_AsksGoEnvForTheCacheOnceAndUsesItForTheSizeLineToo(t *testing.T) {
	withFreeSpace(t, 12)
	repo := doctordiskFixture(t, 3<<20, 7<<20, 1<<20)
	cache := doctorGoCacheDirFn()
	asked := 0
	doctorGoCacheDirFn = func() string { asked++; return cache }

	c := doctorDiskSpace(DoctorInput{Repo: repo})

	if asked != 1 {
		t.Errorf("go env GOCACHE was asked %d times in one doctor run, want 1", asked)
	}
	if !strings.Contains(c.Detail, "go build cache 3.0 MB") {
		t.Errorf("detail = %q, want the size of the cache the one answer named", c.Detail)
	}
}
