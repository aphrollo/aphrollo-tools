package suite

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The record behind the run splitter: how long each Go package's tests took
// the last times they finished, so a package list can be cut into runs that
// each fit their budget without anyone writing the durations down.
//
// gate.log already keeps one duration per gate run, and that is the wrong
// unit for this: the merge gate's twenty-package run is one line, and a
// timeout line holds the budget it was cut off at, not the work. What a plan
// needs is the package's own seconds, which `go test -json` states for every
// package that finishes, so each finished package is recorded under its own
// import path, apart for -race runs (several times dearer than plain ones).
//
// A package a timed-out run left unfinished is recorded too, flagged as cut
// off, at the seconds the run had already spent: a lower bound, never the
// work. Without it a package that never finishes would be planned again with
// the same neighbours and time out again forever; with it the next plan gives
// the suspect a run of its own, which finishes (and records the truth) or
// times out alone and names itself.
//
// The record is statistics, not evidence: a line lost to two writers racing
// costs one estimate one point.

const (
	// pkgSecsKeep bounds the statistic to the most recent samples, so a package
	// that got slower is planned from what it costs now.
	pkgSecsKeep = 20
	// pkgSecsWindow is how far back a sample may come from: recent enough that
	// a package which has since doubled is not planned from its old shape, long
	// enough that a repo merged weekly still has a record.
	pkgSecsWindow = 30 * 24 * time.Hour
)

// pkgSecsCompactAt is the size past which a write rewrites the record down to
// pkgSecsKeep samples per package. A var so a test can shrink it.
var pkgSecsCompactAt int64 = 512 << 10

// pkgSample is one recorded measurement of one package.
type pkgSample struct {
	At   time.Time `json:"at"`
	Pkg  string    `json:"pkg"`
	Race bool      `json:"race"`
	Secs float64   `json:"secs"`
	// Cut marks a sample from a run that ended with this package unfinished:
	// Secs is how long that run had spent, a floor on the package's cost.
	Cut bool `json:"cut,omitempty"`
}

// pkgSecsPath is the record's file, beside gate.log. "" with no state dir.
func pkgSecsPath() string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "pkg-secs.jsonl")
}

// recordPkgSamples appends samples to the record, and compacts it when it has
// outgrown pkgSecsCompactAt. Best-effort: a record that cannot be written
// leaves the next plan on its defaults, which is today's behaviour.
func recordPkgSamples(samples []pkgSample) {
	path := pkgSecsPath()
	if path == "" || len(samples) == 0 {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, s := range samples {
		if enc.Encode(s) != nil {
			return
		}
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, werr := f.Write(buf.Bytes())
	size := int64(0)
	if info, serr := f.Stat(); serr == nil {
		size = info.Size()
	}
	if f.Close() != nil || werr != nil {
		return
	}
	if size > pkgSecsCompactAt {
		compactPkgSecs(path)
	}
}

// readPkgSamples is every parseable sample in the record, in write order. A
// line that does not parse is skipped, the record being text several
// processes append to.
func readPkgSamples(path string) []pkgSample {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []pkgSample
	for line := range strings.Lines(string(data)) {
		var s pkgSample
		if json.Unmarshal([]byte(line), &s) == nil && s.Pkg != "" && s.Secs > 0 {
			out = append(out, s)
		}
	}
	return out
}

// compactPkgSecs rewrites the record keeping each package's newest
// pkgSecsKeep samples (kept apart by -race), in write order.
func compactPkgSecs(path string) {
	all := readPkgSamples(path)
	type key struct {
		pkg  string
		race bool
	}
	kept := map[key]int{}
	keep := make([]bool, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		k := key{all[i].Pkg, all[i].Race}
		if kept[k] < pkgSecsKeep {
			kept[k]++
			keep[i] = true
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i, s := range all {
		if keep[i] && enc.Encode(s) != nil {
			return
		}
	}
	_ = writeFileAtomic(path, buf.Bytes())
}

// recordedPkgSecs is the estimate for every package the record has seen for
// this kind of run: the nearest-rank p90 of its newest pkgSecsKeep samples
// inside the window. A max is brittle (one bad night sets every plan after
// it); a median is blind to the slow tail, which is the one that times a run
// out; the p90 follows the tail and needs more than one slow run to move.
func recordedPkgSecs(race bool) map[string]float64 {
	return recordedPkgSecsAt(race, time.Now())
}

// recordedPkgSecsAt is recordedPkgSecs as of now: a sample exactly a window old
// still counts, one older does not.
func recordedPkgSecsAt(race bool, now time.Time) map[string]float64 {
	path := pkgSecsPath()
	if path == "" {
		return nil
	}
	byPkg := map[string][]float64{}
	for _, s := range readPkgSamples(path) {
		if s.Race != race || now.Sub(s.At) > pkgSecsWindow {
			continue
		}
		byPkg[s.Pkg] = append(byPkg[s.Pkg], s.Secs)
	}
	est := make(map[string]float64, len(byPkg))
	for pkg, secs := range byPkg {
		secs = secs[max(0, len(secs)-pkgSecsKeep):]
		sort.Float64s(secs)
		est[pkg] = secs[int(math.Ceil(0.9*float64(len(secs))))-1]
	}
	return est
}

// goTestEventSecs is the part of a `go test -json` event this record reads.
type goTestEventSecs struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
}

// goTestPackageSecs is the seconds each package that passed took, from a
// `go test -json` stream: the package-level pass event (no Test) carries the
// package's own elapsed. A stream cut off mid-line, which is what a killed run
// leaves, still yields every package that finished before the cut.
func goTestPackageSecs(rawJSON string) map[string]float64 {
	out := map[string]float64{}
	dec := json.NewDecoder(strings.NewReader(rawJSON))
	for {
		var e goTestEventSecs
		if dec.Decode(&e) != nil {
			return out
		}
		if e.Action == "pass" && e.Test == "" && e.Package != "" && e.Elapsed > 0 {
			out[e.Package] = e.Elapsed
		}
	}
}
