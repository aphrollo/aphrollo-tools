package gc

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGCLegacyJobs_ProposesOnlyAWeekIdleJobDirectoriesNoLiveRecordPointsInto(t *testing.T) {
	jobs := t.TempDir()
	stale := filepath.Join(jobs, "job-stale")
	mkFile(t, filepath.Join(stale, "tmp", "base-target", "log.txt"), "x", 10*24*time.Hour)
	fresh := filepath.Join(jobs, "job-fresh")
	mkFile(t, filepath.Join(fresh, "tmp", "base-target", "log.txt"), "x", 24*time.Hour)
	mixed := filepath.Join(jobs, "job-mixed")
	mkFile(t, filepath.Join(mixed, "tmp", "base-target", "old.txt"), "x", 10*24*time.Hour)
	mkFile(t, filepath.Join(mixed, "new.txt"), "x", time.Hour)
	live := filepath.Join(jobs, "job-live")
	mkFile(t, filepath.Join(live, "tmp", "base-target", "log.txt"), "x", 10*24*time.Hour)
	mkFile(t, filepath.Join(jobs, "stray-file.txt"), "x", 10*24*time.Hour)

	got := gcLegacyJobs(jobs, time.Now(), []string{filepath.Join(live, "tmp", "base-target", "log.txt")})

	if len(got) != 1 || got[0].Path != stale || got[0].Kind != GCKindTempLitter || got[0].Size == 0 {
		t.Fatalf("candidates = %+v, want exactly %s", got, stale)
	}
}

func TestGCLegacyJobs_AMissingDirectoryProposesNothing(t *testing.T) {
	if got := gcLegacyJobs(filepath.Join(t.TempDir(), "none"), time.Now(), nil); len(got) != 0 {
		t.Errorf("candidates = %+v", got)
	}
}
