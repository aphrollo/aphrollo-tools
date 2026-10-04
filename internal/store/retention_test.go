package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	if present(s.lockPath("lane/gone")) {
		t.Error("the removed lane's lock file was left behind")
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
