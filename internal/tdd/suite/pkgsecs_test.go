package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolatedState points the gate's state dir at a fresh directory for one test.
func isolatedState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}

func sampleAt(pkg string, secs float64, age time.Duration) pkgSample {
	return pkgSample{At: time.Now().Add(-age), Pkg: pkg, Race: true, Secs: secs}
}

// TestRecordedPkgSecs_IsTheP90OfTheLastTwentyRuns pins the statistic: of the
// 25 samples recorded (1s to 25s), only the last twenty count (6s to 25s),
// and the estimate is their nearest-rank p90, the 18th of 20, which is 23s.
func TestRecordedPkgSecs_IsTheP90OfTheLastTwentyRuns(t *testing.T) {
	isolatedState(t)
	var samples []pkgSample
	for i := 1; i <= 25; i++ {
		samples = append(samples, sampleAt("m/a", float64(i), time.Minute))
	}
	recordPkgSamples(samples)

	got := recordedPkgSecs(true)

	if got["m/a"] != 23 {
		t.Fatalf("estimate for m/a = %v, want 23 (p90 of the last twenty of 25)", got["m/a"])
	}
}

// TestRecordedPkgSecs_KeepsRaceAndPlainRunsApart pins that a -race run's
// seconds never stand in for a plain run's: the same package costs several
// times more under the race detector.
func TestRecordedPkgSecs_KeepsRaceAndPlainRunsApart(t *testing.T) {
	isolatedState(t)
	plain := sampleAt("m/a", 5, time.Minute)
	plain.Race = false
	recordPkgSamples([]pkgSample{sampleAt("m/a", 50, time.Minute), plain})

	if got := recordedPkgSecs(true)["m/a"]; got != 50 {
		t.Errorf("race estimate = %v, want 50", got)
	}
	if got := recordedPkgSecs(false)["m/a"]; got != 5 {
		t.Errorf("plain estimate = %v, want 5", got)
	}
}

// TestRecordedPkgSecs_IgnoresASampleOlderThanThirtyDays pins the window: a
// package that has since doubled in size is not budgeted from its old shape.
func TestRecordedPkgSecs_IgnoresASampleOlderThanThirtyDays(t *testing.T) {
	isolatedState(t)
	recordPkgSamples([]pkgSample{
		sampleAt("m/old", 900, 31*24*time.Hour),
		sampleAt("m/new", 7, 29*24*time.Hour),
	})

	got := recordedPkgSecs(true)

	if _, ok := got["m/old"]; ok {
		t.Errorf("estimate for a 31-day-old sample = %v, want none", got["m/old"])
	}
	if got["m/new"] != 7 {
		t.Errorf("estimate for a 29-day-old sample = %v, want 7", got["m/new"])
	}
}

// TestRecordedPkgSecs_NoRecordIsNoEstimate pins that a box that has never
// recorded a run plans from defaults and never fails.
func TestRecordedPkgSecs_NoRecordIsNoEstimate(t *testing.T) {
	isolatedState(t)

	if got := recordedPkgSecs(true); len(got) != 0 {
		t.Fatalf("estimates with no record = %v, want none", got)
	}
}

// TestRecordPkgSamples_CompactsToTheLastTwentyPerPackage pins that the record
// does not grow without bound: past its size ceiling a write keeps the last
// twenty samples of each package and drops the rest, the newest ones first in
// line to be kept.
func TestRecordPkgSamples_CompactsToTheLastTwentyPerPackage(t *testing.T) {
	dir := isolatedState(t)
	prev := pkgSecsCompactAt
	pkgSecsCompactAt = 200
	t.Cleanup(func() { pkgSecsCompactAt = prev })
	for i := 1; i <= 40; i++ {
		recordPkgSamples([]pkgSample{sampleAt("m/a", float64(i), time.Minute)})
	}

	data, err := os.ReadFile(pkgSecsPath())
	if err != nil {
		t.Fatalf("reading the record under %s: %v", dir, err)
	}
	if lines := strings.Count(string(data), "\n"); lines != pkgSecsKeep {
		t.Fatalf("record holds %d lines after 40 writes with a 200-byte ceiling, want exactly the last %d", lines, pkgSecsKeep)
	}
	if got := recordedPkgSecs(true)["m/a"]; got != 38 {
		t.Fatalf("estimate after compaction = %v, want 38 (p90 of 21..40): the newest samples must survive", got)
	}
}

// TestReadPkgSamples_SkipsWhatItCannotUse pins that the record is read
// leniently: a line that is not JSON, a sample naming no package and one with
// no seconds are skipped, and the good samples around them are kept.
func TestReadPkgSamples_SkipsWhatItCannotUse(t *testing.T) {
	isolatedState(t)
	path := pkgSecsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	lines := strings.Join([]string{
		`{"at":"2026-10-01T00:00:00Z","pkg":"m/a","race":true,"secs":5}`,
		`not json at all`,
		`{"at":"2026-10-01T00:00:00Z","pkg":"","race":true,"secs":5}`,
		`{"at":"2026-10-01T00:00:00Z","pkg":"m/b","race":true,"secs":0}`,
		`{"at":"2026-10-01T00:00:00Z","pkg":"m/c","race":true,"secs":0.5}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got := readPkgSamples(path)

	if len(got) != 2 || got[0].Pkg != "m/a" || got[1].Pkg != "m/c" {
		t.Fatalf("samples = %+v, want m/a and m/c only", got)
	}
}

// TestGoTestPackageSecs_ReadsEachPassedPackageFromTheJSONStream pins the
// reading: a package-level pass event carries the package's own seconds, a
// per-test event and a failed package carry none that count, and a stream cut
// off mid-line (a killed run) still yields what was complete before the cut.
func TestGoTestPackageSecs_ReadsEachPassedPackageFromTheJSONStream(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"m/a","Test":"TestX"}`,
		`{"Action":"pass","Package":"m/a","Test":"TestX","Elapsed":0.5}`,
		`{"Action":"pass","Package":"m/a","Elapsed":12.25}`,
		`{"Action":"fail","Package":"m/b","Elapsed":3}`,
		`{"Action":"skip","Package":"m/c","Elapsed":0}`,
		`{"Action":"pass","Package":"m/d","Elapsed":4}`,
		`{"Action":"pass","Package":"m/z","Elapsed":0}`,
		`{"Action":"pass","Package":"m/e","Elap`,
	}, "\n")

	got := goTestPackageSecs(stream)

	want := map[string]float64{"m/a": 12.25, "m/d": 4}
	if len(got) != len(want) || got["m/a"] != 12.25 || got["m/d"] != 4 {
		t.Fatalf("package seconds = %v, want %v", got, want)
	}
}

// TestPkgSecsPath_LivesBesideTheGateLog pins where the record is kept, so a
// reader looking for it finds it with the rest of the gate's state.
func TestPkgSecsPath_LivesBesideTheGateLog(t *testing.T) {
	dir := isolatedState(t)

	if got, want := pkgSecsPath(), fmt.Sprintf("%s%cgate-state%cpkg-secs.jsonl", dir, os.PathSeparator, os.PathSeparator); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// TestRecordedPkgSecsAt_ASampleExactlyAWindowOldStillCounts pins the edge of
// the window: a sample thirty days to the nanosecond old is used, one
// nanosecond older is not.
func TestRecordedPkgSecsAt_ASampleExactlyAWindowOldStillCounts(t *testing.T) {
	isolatedState(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	recordPkgSamples([]pkgSample{
		{At: now.Add(-pkgSecsWindow), Pkg: "m/edge", Race: true, Secs: 11},
		{At: now.Add(-pkgSecsWindow - time.Nanosecond), Pkg: "m/past", Race: true, Secs: 13},
	})

	got := recordedPkgSecsAt(true, now)

	if got["m/edge"] != 11 {
		t.Errorf("estimate for a sample exactly a window old = %v, want 11", got["m/edge"])
	}
	if _, ok := got["m/past"]; ok {
		t.Errorf("estimate for a sample a nanosecond past the window = %v, want none", got["m/past"])
	}
}

// TestRecordPkgSamples_ARecordExactlyAtTheCeilingIsNotCompacted pins the
// ceiling as a ceiling: a record that reaches it to the byte is left as it is,
// and one byte over is rewritten (the padding line here is one the rewrite
// drops, being no sample).
func TestRecordPkgSamples_ARecordExactlyAtTheCeilingIsNotCompacted(t *testing.T) {
	isolatedState(t)
	prev := pkgSecsCompactAt
	t.Cleanup(func() { pkgSecsCompactAt = prev })
	s := pkgSample{At: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), Pkg: "m/a", Race: true, Secs: 5}
	line, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	pad := strings.Repeat("x", 63) + "\n"
	path := pkgSecsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	total := int64(len(pad) + len(line) + 1)

	for _, tc := range []struct {
		name    string
		ceiling int64
		padKept bool
	}{
		{"at the ceiling", total, true},
		{"one byte over the ceiling", total - 1, false},
	} {
		if err := os.WriteFile(path, []byte(pad), 0o600); err != nil {
			t.Fatalf("%s: setup: %v", tc.name, err)
		}
		pkgSecsCompactAt = tc.ceiling

		recordPkgSamples([]pkgSample{s})

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: reading the record: %v", tc.name, err)
		}
		if kept := strings.Contains(string(data), pad); kept != tc.padKept {
			t.Errorf("%s: padding line kept = %v, want %v (record %q)", tc.name, kept, tc.padKept, data)
		}
		if !strings.Contains(string(data), `"pkg":"m/a"`) {
			t.Errorf("%s: the sample is gone from the record %q", tc.name, data)
		}
	}
}
