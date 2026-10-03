package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var sweepNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// keyN is the nth distinct key.
func keyN(n int) string { return fmt.Sprintf("%064x", n) }

// seed records one law verdict for lane under key and dates the file age ago.
func seed(t *testing.T, s *Store, key, lane string, age time.Duration) {
	t.Helper()
	seedFrom(t, s, key, lane, sweepNow, age)
}

// seedFrom is seed with the age counted back from base.
func seedFrom(t *testing.T, s *Store, key, lane string, base time.Time, age time.Duration) {
	t.Helper()
	if _, err := s.RecordVerdict(bounded(t), key, lane, Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}}); err != nil {
		t.Fatal(err)
	}
	when := base.Add(-age)
	if err := os.Chtimes(s.verdictPath(key), when, when); err != nil {
		t.Fatal(err)
	}
}

func exists(s *Store, key string) bool {
	_, err := os.Stat(s.verdictPath(key))
	return err == nil
}

func TestSweepVerdicts_removesWhatIsPastFourteenDaysAndKeepsTheRest(t *testing.T) {
	s := open(t, t.TempDir())
	seed(t, s, keyN(1), "", 15*24*time.Hour)
	seed(t, s, keyN(2), "", 13*24*time.Hour)
	got, err := s.SweepVerdicts(SweepOptions{Now: sweepNow})
	if err != nil {
		t.Fatal(err)
	}
	if exists(s, keyN(1)) || !exists(s, keyN(2)) {
		t.Errorf("15-day file present=%v, 13-day file present=%v; want gone and kept", exists(s, keyN(1)), exists(s, keyN(2)))
	}
	if got.Expired != 1 || got.Closed != 0 || got.Evicted != 0 || got.Kept != 1 {
		t.Errorf("result = %+v, want 1 expired and 1 kept", got)
	}
}

func TestSweepVerdicts_aVerdictOfOnlyClosedLanesGoesBeforeItsTime(t *testing.T) {
	s := open(t, t.TempDir())
	closed := map[string]bool{"lane/done": true, "lane/also-done": true}
	also := func(key, lane string) {
		t.Helper()
		if _, err := s.RecordVerdict(bounded(t), key, lane, Verdict{Laws: []LawVerdict{{Law: "m", Result: LawPass}}}); err != nil {
			t.Fatal(err)
		}
	}
	seed(t, s, keyN(1), "lane/done", time.Hour)
	also(keyN(1), "lane/also-done")
	seed(t, s, keyN(2), "lane/done", time.Hour)
	also(keyN(2), "lane/open")
	seed(t, s, keyN(3), "", time.Hour)
	// The extra writes dated the files from now; date them from the sweep's clock again.
	for _, k := range []string{keyN(1), keyN(2)} {
		when := sweepNow.Add(-time.Hour)
		if err := os.Chtimes(s.verdictPath(k), when, when); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.SweepVerdicts(SweepOptions{Now: sweepNow, Closed: func(l string) bool { return closed[l] }})
	if err != nil {
		t.Fatal(err)
	}
	if exists(s, keyN(1)) {
		t.Error("a verdict of closed lanes only was kept")
	}
	if !exists(s, keyN(2)) {
		t.Error("a verdict one open lane still wrote was removed")
	}
	if !exists(s, keyN(3)) {
		t.Error("a verdict of no lane was removed: it has no lane to close")
	}
	if got.Closed != 1 || got.Kept != 2 {
		t.Errorf("result = %+v, want 1 closed and 2 kept", got)
	}
}

func TestSweepVerdicts_theCapEvictsTheLeastRecentlyUsedFirst(t *testing.T) {
	s := open(t, t.TempDir())
	for i := 1; i <= 5; i++ {
		seed(t, s, keyN(i), "", time.Duration(6-i)*24*time.Hour) // key 1 is the oldest, key 5 the newest
	}
	got, err := s.SweepVerdicts(SweepOptions{Now: sweepNow, Cap: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{false, false, true, true, true} {
		if exists(s, keyN(i+1)) != want {
			t.Errorf("key %d present = %v, want %v", i+1, exists(s, keyN(i+1)), want)
		}
	}
	if got.Evicted != 2 || got.Kept != 3 {
		t.Errorf("result = %+v, want 2 evicted and 3 kept", got)
	}
}

func TestSweepVerdicts_theDefaultsAre2000FilesAndFourteenDays(t *testing.T) {
	if VerdictCap != 2000 || VerdictMaxAge != 14*24*time.Hour {
		t.Errorf("cap %d, max age %v; the architecture says 2,000 files and 14 days", VerdictCap, VerdictMaxAge)
	}
}

func TestReadVerdict_aReadFileOutlivesAnUnreadNewerOne(t *testing.T) {
	s := open(t, t.TempDir())
	now := time.Now()
	seedFrom(t, s, keyN(1), "", now, 8*24*time.Hour)
	seedFrom(t, s, keyN(2), "", now, 2*24*time.Hour)
	// The older file is the one a stage just reused. Its read dates it from now.
	if _, ok := read(t, s, keyN(1)); !ok {
		t.Fatal("seeded verdict is absent")
	}
	if _, err := s.SweepVerdicts(SweepOptions{Now: now, Cap: 1}); err != nil {
		t.Fatal(err)
	}
	if !exists(s, keyN(1)) || exists(s, keyN(2)) {
		t.Errorf("after a cap of 1: read file present=%v, unread newer file present=%v; want kept and evicted", exists(s, keyN(1)), exists(s, keyN(2)))
	}
}

func TestReadVerdict_aRecentlyUsedFileIsNotRewrittenOnEveryRead(t *testing.T) {
	s := open(t, t.TempDir())
	seedFrom(t, s, keyN(1), "", time.Now(), time.Minute)
	before, _ := os.Stat(s.verdictPath(keyN(1)))
	read(t, s, keyN(1))
	after, _ := os.Stat(s.verdictPath(keyN(1)))
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a read a minute after the last use moved the time from %v to %v", before.ModTime(), after.ModTime())
	}
}

func TestSweepVerdicts_clearsAbandonedTempFilesAndLeavesTheLockAndStrangers(t *testing.T) {
	s := open(t, t.TempDir())
	seed(t, s, keyN(1), "", time.Hour)
	dir := filepath.Dir(s.verdictPath(keyN(1)))
	stale := filepath.Join(dir, keyN(2)+".json.tmp123")
	fresh := filepath.Join(dir, keyN(3)+".json.tmp456")
	stranger := filepath.Join(dir, "notes.txt")
	old := sweepNow.Add(-30 * 24 * time.Hour)
	for _, f := range []string{stale, fresh, stranger} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(fresh, sweepNow, sweepNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SweepVerdicts(SweepOptions{Now: sweepNow}); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]bool{stale: false, fresh: true, stranger: true, s.verdictLockPath(): true} {
		if _, err := os.Stat(f); (err == nil) != want {
			t.Errorf("%s present = %v, want %v", filepath.Base(f), err == nil, want)
		}
	}
}

func TestSweepVerdicts_aDamagedFileAgesOutLikeAnyOther(t *testing.T) {
	s := open(t, t.TempDir())
	plant(t, s, keyN(1), "{torn")
	when := sweepNow.Add(-20 * 24 * time.Hour)
	if err := os.Chtimes(s.verdictPath(keyN(1)), when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SweepVerdicts(SweepOptions{Now: sweepNow, Closed: func(string) bool { return true }}); err != nil {
		t.Fatal(err)
	}
	if exists(s, keyN(1)) {
		t.Error("a torn file past its time was kept")
	}
}

func TestSweepVerdicts_aDamagedFileIsNotAClosedLaneEvenWhenAllLanesAre(t *testing.T) {
	s := open(t, t.TempDir())
	plant(t, s, keyN(1), "{torn")
	when := sweepNow.Add(-time.Hour)
	if err := os.Chtimes(s.verdictPath(keyN(1)), when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SweepVerdicts(SweepOptions{Now: sweepNow, Closed: func(string) bool { return true }}); err != nil {
		t.Fatal(err)
	}
	if !exists(s, keyN(1)) {
		t.Error("a damaged file naming no lane was removed as a closed lane's")
	}
}

func TestSweepVerdicts_aStoreWithNoVerdictsIsFine(t *testing.T) {
	s := open(t, t.TempDir())
	got, err := s.SweepVerdicts(SweepOptions{Now: sweepNow})
	if err != nil || got != (SweepResult{}) {
		t.Errorf("SweepVerdicts = %+v, %v; want the zero result", got, err)
	}
}
