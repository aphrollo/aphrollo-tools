package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A deferred job's record, log and result are keyed by project AND session,
// so every session that ever deferred a build in any repo left three files
// behind and nothing ever read them again. The ceiling on a job is 600s, so
// anything a day old is finished, abandoned, or from a session that ended.

// deferredJobFilesFor writes the three files one job leaves and returns them.
func deferredJobFilesFor(t *testing.T, session, root string, age time.Duration) []string {
	t.Helper()
	saveDeferredJob(DeferredJob{Project: root, Session: session, Phase: "build", Started: time.Now().Add(-age)})
	j, ok := loadDeferredJob(session, root)
	if !ok {
		t.Fatal("setup: the job was not saved")
	}
	files := []string{deferredJobPath(session, root), j.Log, j.Result}
	for _, p := range files {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		old := time.Now().Add(-age)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return files
}

func TestSweepDeferredJobs_DropsADayOldRecordAndKeepsAFreshOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stale := deferredJobFilesFor(t, "gone", filepath.Join(t.TempDir(), "old-lane"), 30*time.Hour)
	fresh := deferredJobFilesFor(t, "live", filepath.Join(t.TempDir(), "this-lane"), time.Minute)

	sweepDeferredJobs(time.Now())

	for _, p := range stale {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s outlived the session that wrote it (stat err = %v)", filepath.Base(p), err)
		}
	}
	for _, p := range fresh {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a job from minutes ago must survive: %s: %v", filepath.Base(p), err)
		}
	}
}

// Loading a job is what every hook and every statusline render does, so it is
// where the sweep costs nothing extra: the directory is already being read.
func TestLoadDeferredJob_SweepsWhatNobodyWillReadAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stale := deferredJobFilesFor(t, "gone", filepath.Join(t.TempDir(), "old-lane"), 30*time.Hour)
	root := filepath.Join(t.TempDir(), "this-lane")

	resetDeferredSweepForTest()
	if _, ok := loadDeferredJob("live", root); ok {
		t.Fatal("setup: no job for this session")
	}

	if _, err := os.Stat(stale[0]); !os.IsNotExist(err) {
		t.Errorf("a day-old record must be swept on the next read (stat err = %v)", err)
	}
}

// The manual sweep reports them too, so an operator sees where the files went
// rather than finding a directory that quietly empties itself.
func TestScanGC_ProposesADayOldDeferredRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stale := deferredJobFilesFor(t, "gone", filepath.Join(t.TempDir(), "old-lane"), 30*time.Hour)

	var found bool
	for _, c := range ScanGC(t.TempDir(), DefaultGCAge, GCScope{GateDirs: true}) {
		if c.Path == stale[0] {
			found = true
			if !strings.Contains(c.Reason, "deferred") {
				t.Errorf("reason = %q, want it to name what the file is", c.Reason)
			}
		}
	}
	if !found {
		t.Errorf("the scan must propose %s", stale[0])
	}
}
