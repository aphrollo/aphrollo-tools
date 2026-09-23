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

// TestSweepDeferredJobs_KillsALivePIDBeforeDroppingADayOldRecord pins the
// crash backstop: a session that never fired EndSession (killed terminal,
// crash) leaves nothing to reap its own job, and no later hook will ever
// read a record keyed to a session that is gone. The 24h sweep is the only
// thing left that will ever see it — so it must kill the PID the record
// still names, not just delete the evidence of it.
func TestSweepDeferredJobs_KillsALivePIDBeforeDroppingADayOldRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "crashed-lane")
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-crashed", Phase: "run", PID: 9009,
		Started: time.Now().Add(-30 * time.Hour),
	})
	path := deferredJobPath("sess-crashed", root)
	old := time.Now().Add(-30 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var killed []int
	t.Cleanup(SetKillDeferredForTest(func(j DeferredJob) { killed = append(killed, j.PID) }))

	sweepDeferredJobs(time.Now())

	if len(killed) != 1 || killed[0] != 9009 {
		t.Fatalf("killed = %v, want exactly [9009]", killed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the day-old record must still be dropped (stat err = %v)", err)
	}
}

// TestSweep_DoesNotKillAPIDTheOSHasRecycled pins the cold-review fix: a
// day-old record's PID is not enough on its own. Between the record's own
// timestamp and the 24h sweep, the OS can hand that same integer to an
// unrelated process — this test simulates exactly that by recording one
// creation time and having the live query answer with a different one — and
// the sweep must withhold the kill while still dropping the stale record.
func TestSweep_DoesNotKillAPIDTheOSHasRecycled(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "recycled-lane")
	recordedCreatedAt := time.Now().Add(-30 * time.Hour)
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-recycled", Phase: "run", PID: 9010,
		Started: recordedCreatedAt, PIDCreatedAt: recordedCreatedAt,
	})
	path := deferredJobPath("sess-recycled", root)
	old := time.Now().Add(-30 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// The OS reports pid 9010 as belonging to a process that started two
	// hours ago -- not the one this record names, which is a day old.
	t.Cleanup(SetProcessStartTimeForTest(func(pid int) (time.Time, bool) {
		if pid == 9010 {
			return time.Now().Add(-2 * time.Hour), true
		}
		return time.Time{}, false
	}))

	var killed []int
	t.Cleanup(SetKillDeferredForTest(func(j DeferredJob) { killed = append(killed, j.PID) }))

	sweepDeferredJobs(time.Now())

	if len(killed) != 0 {
		t.Fatalf("killed = %v, want no kill: pid 9010 was recycled by the OS", killed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the day-old record must still be dropped even when the kill is withheld (stat err = %v)", err)
	}
}

// TestSweepDeferredJobs_ReturnsCountOfFilesRemoved pins the return value, not
// just the on-disk effect: every one of a day-old job's three files (record,
// log, result) is a real removal the caller's count must reflect.
func TestSweepDeferredJobs_ReturnsCountOfFilesRemoved(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stale := deferredJobFilesFor(t, "gone", filepath.Join(t.TempDir(), "old-lane"), 30*time.Hour)

	got := sweepDeferredJobs(time.Now())

	want := 3 // record + log + result, each older than the 24h ceiling
	if got != want {
		t.Fatalf("removed = %d, want %d (files were %v)", got, want, stale)
	}
}

// TestKillLivePID_SkipsAZeroPID pins the guard the doc comment names: a
// record decoding to pid 0 means nothing was ever recorded as spawned, so
// killLivePID must leave it alone rather than handing pid 0 to the killer.
func TestKillLivePID_SkipsAZeroPID(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "zero-pid-lane")
	saveDeferredJob(DeferredJob{Project: root, Session: "sess-zero", Phase: "run", PID: 0, Started: time.Now()})
	path := deferredJobPath("sess-zero", root)

	var killed []int
	t.Cleanup(SetKillDeferredForTest(func(j DeferredJob) { killed = append(killed, j.PID) }))

	killLivePID(path)

	if len(killed) != 0 {
		t.Fatalf("killed = %v, want no kill: a record with pid 0 names no process", killed)
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
