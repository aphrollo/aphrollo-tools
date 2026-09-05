package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- --stale sweep: detached, PR-less, idle worktrees ---

// stubNow pins the package's clock seam to a fixed instant, so an age
// comparison built from it lands on an exact day boundary instead of racing
// the wall clock.
func stubNow(t *testing.T, at time.Time) {
	t.Helper()
	ov := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = ov })
}

// detachedIdleWorktree builds a repo with one worktree, commits into it dated
// ageDays before at, detaches HEAD there, and back-dates every file's mtime
// to match — the exact shape --stale's candidate rule inspects. Returns the
// repo toplevel and the worktree path.
func detachedIdleWorktree(t *testing.T, at time.Time, ageDays int) (repo, wt string) {
	t.Helper()
	repo, wt, _ = preparedRepo(t)
	if err := os.WriteFile(filepath.Join(wt, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wt, "add", ".")
	commitDate := at.AddDate(0, 0, -ageDays).Format(time.RFC3339)
	t.Setenv("GIT_COMMITTER_DATE", commitDate)
	t.Setenv("GIT_AUTHOR_DATE", commitDate)
	gitRun(t, wt, "commit", "-q", "-m", "ticket work")
	gitRun(t, wt, "checkout", "-q", "--detach")

	touch := at.AddDate(0, 0, -ageDays)
	entries, err := os.ReadDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.Chtimes(filepath.Join(wt, e.Name()), touch, touch); err != nil {
			t.Fatal(err)
		}
	}
	return repo, wt
}

// TestPrune_StaleRemovesADetachedPRLessTreeOlderThanTheBar: a detached,
// PR-less, clean worktree whose commit and files are older than the --stale
// bar is removed; the identical shape at 1 day (under a 3d bar) is kept, with
// the skip line naming the age.
func TestPrune_StaleRemovesADetachedPRLessTreeOlderThanTheBar(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	stubNow(t, at)
	stubPRState(t, func(_, _ string) (string, error) { return "", nil })

	t.Run("older than the bar is removed", func(t *testing.T) {
		repo, wt := detachedIdleWorktree(t, at, 5)
		s, err := StaleSweepPlan(repo, 3*24*time.Hour)
		if err != nil {
			t.Fatalf("StaleSweepPlan: %v", err)
		}
		var out, errb bytes.Buffer
		if err := s.Run(true, &out, &errb); err != nil {
			t.Fatalf("Run: %v\n%s", err, errb.String())
		}
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Errorf("stale worktree should be gone, stat err = %v", err)
		}
	})

	t.Run("under the bar is kept and names the age", func(t *testing.T) {
		repo, wt := detachedIdleWorktree(t, at, 1)
		s, err := StaleSweepPlan(repo, 3*24*time.Hour)
		if err != nil {
			t.Fatalf("StaleSweepPlan: %v", err)
		}
		var out, errb bytes.Buffer
		if err := s.Run(true, &out, &errb); err != nil {
			t.Fatalf("Run: %v\n%s", err, errb.String())
		}
		if _, err := os.Stat(wt); err != nil {
			t.Fatalf("worktree under the bar must survive: %v", err)
		}
		if !strings.Contains(out.String(), "skip:") || !strings.Contains(out.String(), "1d") {
			t.Errorf("skip line should name the age (1d):\n%s", out.String())
		}
	})
}

// TestPrune_StaleKeepsADirtyDetachedTree: an uncommitted change keeps a
// detached, otherwise-stale worktree, naming "dirty" in the skip reason.
func TestPrune_StaleKeepsADirtyDetachedTree(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	stubNow(t, at)
	stubPRState(t, func(_, _ string) (string, error) { return "", nil })
	repo, wt := detachedIdleWorktree(t, at, 5)
	dirty(t, wt)

	s, err := StaleSweepPlan(repo, 3*24*time.Hour)
	if err != nil {
		t.Fatalf("StaleSweepPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := s.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("dirty worktree must survive: %v", err)
	}
	if !strings.Contains(out.String(), "skip:") || !strings.Contains(out.String(), "dirty") {
		t.Errorf("skip line should name dirty:\n%s", out.String())
	}
}

// TestPrune_StaleKeepsATreeWithALaneMarker: a <tree>.lane marker beside the
// worktree keeps it regardless of age.
func TestPrune_StaleKeepsATreeWithALaneMarker(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	stubNow(t, at)
	stubPRState(t, func(_, _ string) (string, error) { return "", nil })
	repo, wt := detachedIdleWorktree(t, at, 5)
	if err := os.WriteFile(wt+".lane", []byte("lane/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := StaleSweepPlan(repo, 3*24*time.Hour)
	if err != nil {
		t.Fatalf("StaleSweepPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := s.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("lane-marked worktree must survive: %v", err)
	}
}

// TestParseStaleDuration_AcceptsDayShorthandAndGoSyntax proves the "3d"
// custom spelling parses as 3*24h and a plain Go duration still parses as
// itself — time.ParseDuration alone rejects "3d" ("unknown unit d").
func TestParseStaleDuration_AcceptsDayShorthandAndGoSyntax(t *testing.T) {
	got, err := ParseStaleDuration("3d")
	if err != nil {
		t.Fatalf("ParseStaleDuration(3d): %v", err)
	}
	if want := 72 * time.Hour; got != want {
		t.Errorf("ParseStaleDuration(3d) = %v, want %v", got, want)
	}
	got, err = ParseStaleDuration("48h")
	if err != nil {
		t.Fatalf("ParseStaleDuration(48h): %v", err)
	}
	if want := 48 * time.Hour; got != want {
		t.Errorf("ParseStaleDuration(48h) = %v, want %v", got, want)
	}
}
