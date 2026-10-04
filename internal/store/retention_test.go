package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

var retNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// put writes a file of size bytes dated age before retNow, making its directory.
func put(t *testing.T, path string, size int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	when := retNow.Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func present(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// laneAt commits a lane in the given life and dates its checkpoint age before retNow.
func laneAt(t *testing.T, s *Store, lane string, life kernel.Life, age time.Duration) {
	t.Helper()
	rec := Record{Lane: kernel.State{Branch: lane, Life: life}}
	if _, err := s.Commit(bounded(t), lane, 0, rec, nil); err != nil {
		t.Fatal(err)
	}
	when := retNow.Add(-age)
	if err := os.Chtimes(s.checkpointPath(lane), when, when); err != nil {
		t.Fatal(err)
	}
}

func retain(t *testing.T, s *Store, opts RetentionOptions) RetentionReport {
	t.Helper()
	opts.Now = retNow
	rep, err := s.Retain(bounded(t), opts)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	return rep
}

func TestRetain_eventMonthsPastSixteenWeeksGoAndTheRestStay(t *testing.T) {
	s := open(t, t.TempDir())
	// Sixteen weeks is 112 days: a month that ended before 2026-06-30 is past it.
	old := filepath.Join(s.dir, "events-2026-05.jsonl")     // ended 2026-06-01
	edge := filepath.Join(s.dir, "events-2026-07.jsonl")    // ended 2026-08-01
	current := filepath.Join(s.dir, "events-2026-10.jsonl") // open
	stranger := filepath.Join(s.dir, "events-notes.jsonl")  // not a month
	for _, f := range []string{old, edge, current, stranger} {
		put(t, f, 10, 0)
	}
	retain(t, s, RetentionOptions{})
	for f, want := range map[string]bool{old: false, edge: true, current: true, stranger: true} {
		if present(f) != want {
			t.Errorf("%s present = %v, want %v", filepath.Base(f), present(f), want)
		}
	}
}

func TestRetain_aRemovedLaneGoesSevenDaysAfterRemovalAndNoLiveLaneDoes(t *testing.T) {
	s := open(t, t.TempDir())
	laneAt(t, s, "lane/gone", kernel.LifeRemoved, 8*day)
	laneAt(t, s, "lane/fresh-gone", kernel.LifeRemoved, 6*day)
	laneAt(t, s, "lane/idle-open", kernel.LifeOpen, 90*day)
	laneAt(t, s, "lane/idle-pr", kernel.LifePR, 90*day)
	retain(t, s, RetentionOptions{})
	for lane, want := range map[string]bool{"lane/gone": false, "lane/fresh-gone": true, "lane/idle-open": true, "lane/idle-pr": true} {
		if present(s.checkpointPath(lane)) != want {
			t.Errorf("checkpoint of %s present = %v, want %v", lane, present(s.checkpointPath(lane)), want)
		}
	}
}

func TestRetain_aLaneInTheMiddleOfACommitIsLeftAlone(t *testing.T) {
	s := open(t, t.TempDir())
	laneAt(t, s, "lane/gone", kernel.LifeRemoved, 8*day)
	release, err := s.lock(bounded(t), "lane/gone")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	retain(t, s, RetentionOptions{})
	if !present(s.checkpointPath("lane/gone")) {
		t.Error("a checkpoint whose lock another process holds was removed")
	}
}

func TestRetain_aVerdictOfARemovedLaneGoesAtOnceAndAnOpenLanesStays(t *testing.T) {
	s := open(t, t.TempDir())
	laneAt(t, s, "lane/gone", kernel.LifeRemoved, 1*day)
	laneAt(t, s, "lane/live", kernel.LifeOpen, 1*day)
	seedFrom(t, s, keyN(1), "lane/gone", retNow, time.Hour)
	seedFrom(t, s, keyN(2), "lane/live", retNow, time.Hour)
	seedFrom(t, s, keyN(3), "lane/unknown", retNow, time.Hour)
	retain(t, s, RetentionOptions{})
	for k, want := range map[string]bool{keyN(1): false, keyN(2): true, keyN(3): true} {
		if exists(s, k) != want {
			t.Errorf("verdict %s present = %v, want %v", k[len(k)-2:], exists(s, k), want)
		}
	}
}

func TestRetain_aDirectoryRuleTakesWhatIsPastItsAgeAndHoldsWhatIsKept(t *testing.T) {
	s := open(t, t.TempDir())
	dir := filepath.Join(s.dir, "jobs")
	put(t, filepath.Join(dir, "old.log"), 5, 25*time.Hour)
	put(t, filepath.Join(dir, "new.log"), 5, 23*time.Hour)
	put(t, filepath.Join(dir, "running.json"), 5, 30*time.Hour)
	put(t, filepath.Join(dir, "sub", "old2.log"), 5, 48*time.Hour)
	keep := func(p string) bool { return filepath.Base(p) == "running.json" }
	rep := retain(t, s, RetentionOptions{Rules: []DirRule{{Name: "jobs", Dir: dir, MaxAge: JobMaxAge, Keep: keep}}})
	for f, want := range map[string]bool{"old.log": false, "new.log": true, "running.json": true, filepath.Join("sub", "old2.log"): false} {
		if present(filepath.Join(dir, f)) != want {
			t.Errorf("%s present = %v, want %v", f, present(filepath.Join(dir, f)), want)
		}
	}
	if len(rep.Removed) != 2 {
		t.Errorf("report lists %d removals, want 2: %+v", len(rep.Removed), rep.Removed)
	}
}

func TestRetain_aByteCapEvictsTheLeastRecentlyUsedFirstAndNeverWhatIsKept(t *testing.T) {
	s := open(t, t.TempDir())
	dir := filepath.Join(s.dir, "out")
	for i := range 5 {
		put(t, filepath.Join(dir, fmt.Sprintf("f%d", i)), 100, time.Duration(5-i)*time.Hour) // f0 oldest
	}
	keep := func(p string) bool { return filepath.Base(p) == "f0" }
	retain(t, s, RetentionOptions{Rules: []DirRule{{Name: "out", Dir: dir, MaxBytes: 250, Keep: keep}}})
	var left []string
	for i := range 5 {
		if present(filepath.Join(dir, fmt.Sprintf("f%d", i))) {
			left = append(left, fmt.Sprintf("f%d", i))
		}
	}
	if want := []string{"f0", "f4"}; !slices.Equal(left, want) {
		t.Errorf("left %v, want %v: f0 is kept, f1 to f3 are evicted oldest first until 300 bytes fit 250", left, want)
	}
}

func TestRetain_dryReportsWhatItWouldRemoveAndRemovesNothing(t *testing.T) {
	s := open(t, t.TempDir())
	old := filepath.Join(s.dir, "events-2026-01.jsonl")
	put(t, old, 10, 0)
	laneAt(t, s, "lane/gone", kernel.LifeRemoved, 8*day)
	seedFrom(t, s, keyN(1), "", retNow, 20*day)
	dir := filepath.Join(s.dir, "out")
	put(t, filepath.Join(dir, "x"), 7, 3*day)
	rep := retain(t, s, RetentionOptions{Dry: true, Rules: []DirRule{{Name: "out", Dir: dir, MaxAge: day}}})
	if !present(old) || !present(s.checkpointPath("lane/gone")) || !exists(s, keyN(1)) || !present(filepath.Join(dir, "x")) {
		t.Error("a dry sweep removed something")
	}
	if len(rep.Removed) != 4 {
		t.Errorf("a dry sweep lists %d removals, want the event month, the lane, the verdict and the out file: %+v", len(rep.Removed), rep.Removed)
	}
}

func TestRetain_aMissingDirectoryIsNothingToSweep(t *testing.T) {
	s := open(t, t.TempDir())
	rep := retain(t, s, RetentionOptions{Rules: []DirRule{{Name: "out", Dir: filepath.Join(s.dir, "nope"), MaxAge: day}}})
	if len(rep.Removed) != 0 {
		t.Errorf("removed %+v from nothing", rep.Removed)
	}
}

func TestRetain_aSweptLanesEventsMonthIsNotNeededToLoadItsLiveNeighbours(t *testing.T) {
	// A live lane whose checkpoint points into an event month that has been
	// swept still loads: the checkpoint is the record, the log only the way back.
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	names, _ := filepath.Glob(filepath.Join(s.dir, "events-*.jsonl"))
	if len(names) != 1 {
		t.Fatalf("event files = %v", names)
	}
	if err := os.Remove(names[0]); err != nil {
		t.Fatal(err)
	}
	rec, ver, err := open(t, s.dir).Load(bounded(t), "fix")
	if err != nil || ver != 1 || len(rec.Lane.Actors) != 1 {
		t.Errorf("Load after the log month went = %+v, %d, %v; want the checkpoint's lane at version 1", rec, ver, err)
	}
}

// TestRetain_stateStaysUnderItsCapsAfterChurn is the F24-F27 measure: after a
// synthetic run of churn the directory holds no more than its caps say.
func TestRetain_stateStaysUnderItsCapsAfterChurn(t *testing.T) {
	s := open(t, t.TempDir())
	const capBytes = 64 << 10
	out, cache := filepath.Join(s.dir, "out"), filepath.Join(s.dir, "cache")

	for m := range 10 { // ten months of logs, 2026-01 .. 2026-10
		put(t, filepath.Join(s.dir, fmt.Sprintf("events-2026-%02d.jsonl", m+1)), 100, 0)
	}
	for i := range 40 {
		life := kernel.LifeRemoved
		if i%4 == 0 {
			life = kernel.LifeOpen
		}
		laneAt(t, s, fmt.Sprintf("lane/l%02d", i), life, time.Duration(i)*day)
	}
	for i := range VerdictCap + 500 {
		seedFrom(t, s, keyN(i+1), "", retNow, time.Duration(i%13)*day) // all inside 14 days; only the cap trims
	}
	for i := range 300 {
		put(t, filepath.Join(out, fmt.Sprintf("o%03d", i)), 1<<10, time.Duration(i)*time.Minute)
		put(t, filepath.Join(cache, fmt.Sprintf("c%03d", i)), 1<<10, time.Duration(i)*time.Minute)
	}
	rules := []DirRule{
		{Name: "out", Dir: out, MaxAge: JobMaxAge, MaxBytes: capBytes},
		{Name: "cache", Dir: cache, MaxBytes: capBytes},
	}
	retain(t, s, RetentionOptions{Rules: rules})

	if n := len(mustGlob(t, filepath.Join(s.dir, "verdicts", "*.json"))); n > VerdictCap {
		t.Errorf("%d verdict files, cap %d", n, VerdictCap)
	}
	for _, f := range mustGlob(t, filepath.Join(s.dir, "events-*.jsonl")) {
		if filepath.Base(f) < "events-2026-06.jsonl" { // 2026-05 ended 142 days ago
			t.Errorf("%s survived past 16 weeks", filepath.Base(f))
		}
	}
	for i := range 40 {
		lane := fmt.Sprintf("lane/l%02d", i)
		gone := i%4 != 0 && i > 7
		if present(s.checkpointPath(lane)) == gone {
			t.Errorf("lane %s (age %dd) present = %v, want %v", lane, i, present(s.checkpointPath(lane)), !gone)
		}
	}
	for _, line := range s.StateSizes(rules) {
		if line.Name == "out" || line.Name == "cache" {
			if line.Bytes > capBytes {
				t.Errorf("%s holds %d bytes, cap %d", line.Name, line.Bytes, capBytes)
			}
			if line.Bytes < capBytes-(2<<10) {
				t.Errorf("%s holds %d bytes: eviction went well below its %d cap", line.Name, line.Bytes, capBytes)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(out, "o000")); err != nil {
		t.Error("the newest out file was evicted before older ones")
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStateSizes_countsEachKindOfFileTheLayoutNames(t *testing.T) {
	s := open(t, t.TempDir())
	put(t, filepath.Join(s.dir, "events-2026-10.jsonl"), 30, 0)
	laneAt(t, s, "lane/a", kernel.LifeOpen, 0)
	seedFrom(t, s, keyN(1), "", retNow, 0)
	put(t, filepath.Join(s.dir, "out", "x"), 7, 0)
	got := map[string]SizeLine{}
	for _, l := range s.StateSizes([]DirRule{{Name: "out", Dir: filepath.Join(s.dir, "out"), MaxBytes: 200 << 20}}) {
		got[l.Name] = l
	}
	if got["events"].Files != 1 || got["events"].Bytes != 30 {
		t.Errorf("events = %+v, want 1 file of 30 bytes", got["events"])
	}
	if got["lanes"].Files != 1 || got["verdicts"].Files != 1 {
		t.Errorf("lanes %+v, verdicts %+v, want a file each", got["lanes"], got["verdicts"])
	}
	if got["out"].Files != 1 || got["out"].Bytes != 7 || got["out"].Cap != 200<<20 {
		t.Errorf("out = %+v, want 1 file of 7 bytes under a 200 MiB cap", got["out"])
	}
}

var _ = context.Background

// oldMonth moves the current month's event file to the month name, as if it had been written then.
func oldMonth(t *testing.T, s *Store, name string) string {
	t.Helper()
	from := filepath.Join(s.dir, "events-"+time.Now().UTC().Format("2006-01")+".jsonl")
	to := filepath.Join(s.dir, name)
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	return to
}

func TestRetain_aMonthHoldingALiveLanesEventsIsKeptAndTheLaneStillRefolds(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("live", "s1/a"))
	mustFact(t, s, entered("live", "s2/a"))
	month := oldMonth(t, s, "events-2026-05.jsonl") // 20 weeks before retNow
	want, _, err := s.Load(bounded(t), "live")
	if err != nil {
		t.Fatal(err)
	}

	retain(t, s, RetentionOptions{})
	if !present(month) {
		t.Fatal("the month that holds a live lane's events was removed")
	}
	// Lose the checkpoint: the refold must give the same record from the kept log.
	if err := os.Remove(s.checkpointPath("live")); err != nil {
		t.Fatal(err)
	}
	got, _, err := open(t, s.dir).Load(bounded(t), "live")
	if err != nil || !reflect.DeepEqual(got.Lane.Actors, want.Lane.Actors) || got.Lane.Life != want.Lane.Life {
		t.Errorf("refold after the sweep = %+v, %v; want actors %v", got.Lane, err, want.Lane.Actors)
	}
}

func TestRetain_aMonthOfOnlyRemovedLanesGoesAndOneSharedWithALiveLaneStays(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("dead", "s1/a"))
	mustFact(t, s, kernel.Event{Kind: kernel.KindLaneRemoved, Lane: "dead", Actor: "s1/a", At: t0})
	if rec, _, _ := s.Load(bounded(t), "dead"); rec.Lane.Life != kernel.LifeRemoved {
		t.Fatalf("lane life = %q, the fixture needs a removed lane", rec.Lane.Life)
	}
	only := oldMonth(t, s, "events-2026-04.jsonl")
	mustFact(t, s, entered("dead2", "s1/a"))
	mustFact(t, s, kernel.Event{Kind: kernel.KindLaneRemoved, Lane: "dead2", Actor: "s1/a", At: t0})
	mustFact(t, s, entered("live", "s1/a"))
	shared := oldMonth(t, s, "events-2026-03.jsonl")

	retain(t, s, RetentionOptions{})
	if present(only) {
		t.Error("a month holding only removed lanes' events was kept")
	}
	if !present(shared) {
		t.Error("a month holding a live lane's events was removed")
	}
}

func TestRetain_aLanesEventsWithNoCheckpointAreJudgedByRefolding(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("gone", "s1/a"))
	mustFact(t, s, kernel.Event{Kind: kernel.KindLaneRemoved, Lane: "gone", Actor: "s1/a", At: t0})
	month := oldMonth(t, s, "events-2026-02.jsonl")
	if err := os.Remove(s.checkpointPath("gone")); err != nil { // swept earlier
		t.Fatal(err)
	}
	retain(t, s, RetentionOptions{})
	if present(month) {
		t.Error("a removed lane whose checkpoint was swept kept its event month for ever")
	}
}

func TestSweptThrough_isTheEndOfTheNewestMonthRemovedAndNeverMovesBack(t *testing.T) {
	s := open(t, t.TempDir())
	if got := SweptThrough(s.dir); !got.IsZero() {
		t.Fatalf("SweptThrough before any sweep = %v, want zero", got)
	}
	put(t, filepath.Join(s.dir, "events-2026-02.jsonl"), 1, 0)
	retain(t, s, RetentionOptions{Dry: true})
	if got := SweptThrough(s.dir); !got.IsZero() {
		t.Errorf("a dry sweep set SweptThrough to %v", got)
	}
	retain(t, s, RetentionOptions{})
	if want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC); !SweptThrough(s.dir).Equal(want) {
		t.Errorf("SweptThrough = %v, want %v", SweptThrough(s.dir), want)
	}
	put(t, filepath.Join(s.dir, "events-2026-01.jsonl"), 1, 0) // a straggler older than what was swept
	retain(t, s, RetentionOptions{})
	if want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC); !SweptThrough(s.dir).Equal(want) {
		t.Errorf("SweptThrough moved back to %v", SweptThrough(s.dir))
	}
}

func TestRetain_theLaneLockFileIsNeverUnlinked(t *testing.T) {
	s := open(t, t.TempDir())
	laneAt(t, s, "lane/gone", kernel.LifeRemoved, 8*day)
	release, err := s.lock(bounded(t), "lane/gone")
	if err != nil {
		t.Fatal(err)
	}
	release()
	retain(t, s, RetentionOptions{})
	if present(s.checkpointPath("lane/gone")) {
		t.Fatal("the removed lane's checkpoint stayed")
	}
	if !present(s.lockPath("lane/gone")) {
		t.Error("the lock file was unlinked: a second holder could then lock a different file")
	}
}

func TestRetain_aFileThatCannotBeRemovedIsNeitherReportedRemovedNorCountedFreed(t *testing.T) {
	s := open(t, t.TempDir())
	dir := filepath.Join(s.dir, "out")
	put(t, filepath.Join(dir, "stuck"), 100, 3*day)
	put(t, filepath.Join(dir, "fine"), 50, 3*day)
	seedFrom(t, s, keyN(1), "", retNow, 20*day)
	stuck := map[string]bool{filepath.Join(dir, "stuck"): true, s.verdictPath(keyN(1)): true}
	defer func(prev func(string) error) { removeFile = prev }(removeFile)
	removeFile = func(p string) error {
		if stuck[p] {
			return errors.New("sharing violation")
		}
		return os.Remove(p)
	}
	var told []string
	rep, err := s.Retain(bounded(t), RetentionOptions{Now: retNow, Rules: []DirRule{{Name: "out", Dir: dir, MaxAge: day}}})
	if err != nil {
		t.Fatalf("Retain err = %v, want nil: failures are in the report, once", err)
	}
	for _, r := range rep.Removed {
		told = append(told, filepath.Base(r.Path))
	}
	if !slices.Equal(told, []string{"fine"}) || rep.Bytes() != 50 {
		t.Errorf("reported removed %v (%d bytes), want only fine (50)", told, rep.Bytes())
	}
	if len(rep.Errors) != 2 {
		t.Errorf("%d errors %v, want one per stuck file", len(rep.Errors), rep.Errors)
	}
}

func TestOpenExisting_createsNothingAndADrySweepLeavesTheDirectoryAsItWas(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "events-2026-01.jsonl"), 3, 0)
	s, err := OpenExisting(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	retain(t, s, RetentionOptions{Dry: true})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("a dry sweep left %v, want only the event file", entries)
	}
}

func TestRetain_aMonthIsKeptAtExactlySixteenWeeksAndGoesJustPastIt(t *testing.T) {
	s := open(t, t.TempDir())
	month := filepath.Join(s.dir, "events-2026-05.jsonl") // ended 2026-06-01
	put(t, month, 5, 0)
	edge := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Add(EventsMaxAge)
	if _, err := s.Retain(bounded(t), RetentionOptions{Now: edge}); err != nil {
		t.Fatal(err)
	}
	if !present(month) {
		t.Fatal("a month exactly 16 weeks after it ended was removed")
	}
	if _, err := s.Retain(bounded(t), RetentionOptions{Now: edge.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	if present(month) {
		t.Error("a month just past 16 weeks was kept")
	}
}

func TestRetain_theMarkerIsRaisedBeforeAMonthGoesAndAFailedWriteKeepsTheMonth(t *testing.T) {
	s := open(t, t.TempDir())
	month := filepath.Join(s.dir, "events-2026-05.jsonl")
	put(t, month, 5, 0)
	prev := writeMarker
	defer func() { writeMarker = prev }()
	writeMarker = func(string, []byte) error { return errors.New("disk full") }
	rep, _ := s.Retain(bounded(t), RetentionOptions{Now: retNow})
	if !present(month) {
		t.Error("a month was removed although its marker could not be written: a reader would take its events for never having happened")
	}
	if len(rep.Errors) != 1 {
		t.Errorf("errors = %v, want the one failed marker write", rep.Errors)
	}

	writeMarker = prev
	// A removal that fails after the marker was raised leaves the marker raised.
	removeFile = func(string) error { return errors.New("sharing violation") }
	defer func() { removeFile = os.Remove }()
	s.Retain(bounded(t), RetentionOptions{Now: retNow})
	if want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC); !SweptThrough(s.dir).Equal(want) {
		t.Errorf("SweptThrough = %v, want %v raised before the removal was tried", SweptThrough(s.dir), want)
	}
}

func TestRetain_aDryVerdictSweepCreatesNoLockFile(t *testing.T) {
	s := open(t, t.TempDir())
	seedFrom(t, s, keyN(1), "", retNow, time.Hour)
	if err := os.Remove(s.verdictLockPath()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	retain(t, s, RetentionOptions{Dry: true})
	if present(s.verdictLockPath()) {
		t.Error("a dry sweep created verdicts/.lock")
	}
}

func TestStateSizes_countsLockFilesAsTheirOwnKind(t *testing.T) {
	s := open(t, t.TempDir())
	laneAt(t, s, "lane/a", kernel.LifeOpen, 0)
	release, err := s.lock(bounded(t), "lane/a")
	if err != nil {
		t.Fatal(err)
	}
	release()
	got := map[string]SizeLine{}
	for _, l := range s.StateSizes(nil) {
		got[l.Name] = l
	}
	if got["locks"].Files != 1 {
		t.Errorf("locks = %+v, want the lane's lock file counted", got["locks"])
	}
}

func TestStateSizes_countsTheVerdictLockToo(t *testing.T) {
	s := open(t, t.TempDir())
	seedFrom(t, s, keyN(1), "", retNow, time.Hour) // a write takes verdicts/.lock
	for _, l := range s.StateSizes(nil) {
		if l.Name == "locks" && l.Files != 1 {
			t.Errorf("locks = %+v, want verdicts/.lock counted once", l)
		}
	}
}
