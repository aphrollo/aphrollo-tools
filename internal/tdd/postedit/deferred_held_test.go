package postedit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDeferredJobStartedWithin_NamesARecentJobOfThatRootAndNoOtherJob(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := filepath.Join(t.TempDir(), "lane")
	other := filepath.Join(t.TempDir(), "other")
	now := time.Now().UTC().Truncate(time.Second)
	saveDeferredJob(DeferredJob{Project: filepath.Join(lane, "crates", "a"), Session: "s1", Phase: "run", Started: now.Add(-4 * time.Minute)})
	saveDeferredJob(DeferredJob{Project: other, Session: "s2", Phase: "run", Started: now})

	got, ok := DeferredJobStartedWithin(lane, 30*time.Minute, now)

	if !ok || !got.Equal(now.Add(-4*time.Minute)) {
		t.Errorf("DeferredJobStartedWithin(lane) = %v, %v; want the job nested in it, started 4 minutes ago", got, ok)
	}
	if _, ok := DeferredJobStartedWithin(filepath.Join(t.TempDir(), "never"), 30*time.Minute, now); ok {
		t.Error("a root nothing was started for reads as held")
	}
}

func TestDeferredJobStartedWithin_AJobOutsideTheWindowIsNotHolding(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := filepath.Join(t.TempDir(), "lane")
	now := time.Now().UTC()
	saveDeferredJob(DeferredJob{Project: lane, Session: "s1", Phase: "build", Started: now.Add(-2 * time.Hour)})

	if got, ok := DeferredJobStartedWithin(lane, 30*time.Minute, now); ok {
		t.Errorf("a job started 2 hours ago reads as held, at %v", got)
	}
}

func TestDeferredJobTargets_NamesWhatARecentRecordPointsAtAndNothingOlder(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	base := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	saveDeferredJob(DeferredJob{Project: filepath.Join(base, "p"), Dir: filepath.Join(base, "d"), Log: filepath.Join(base, "l.log"),
		Result: filepath.Join(base, "r.json"), Session: "s1", Phase: "run", Started: now.Add(-time.Hour)})
	saveDeferredJob(DeferredJob{Project: filepath.Join(base, "old"), Session: "s2", Phase: "run", Started: now.Add(-48 * time.Hour)})

	got := DeferredJobTargets(24*time.Hour, now)

	want := map[string]bool{filepath.Join(base, "p"): true, filepath.Join(base, "d"): true, filepath.Join(base, "l.log"): true, filepath.Join(base, "r.json"): true}
	if len(got) != len(want) {
		t.Fatalf("DeferredJobTargets = %v, want the four paths of the recent record", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected target %q", p)
		}
	}
}
