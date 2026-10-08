// Package testcost is the record of what a repo's tests cost: per-test and
// per-package seconds read out of the output a runner already prints, kept as
// aggregates in the per-repo event log, and the statistics the test_cost law,
// the commit-time info line and the weekly report read back.
//
// Nothing here runs a test or adds a flag to a runner. A language that states
// no per-test time in its default output is recorded per package (Go has both,
// nextest has per-test, cargo per binary, vitest per file); pytest and any
// other runner contribute the suite's own seconds only.
package testcost

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// EventKind is the event-log kind of one suite run's aggregates.
	EventKind = "suite.cost"
	// MinRecordSecs is the floor under which a test or package is not recorded:
	// the record is for the tests worth naming, not for every test.
	MinRecordSecs = 1.0
	// MaxTests and MaxPkgs bound one event to the slowest rows.
	MaxTests = 50
	MaxPkgs  = 50
	// DefaultThresholdSecs is the cost over which a single test is slow.
	DefaultThresholdSecs = 10.0
)

// Run is the cost of one suite run: its seconds, and the seconds of the tests
// and packages (or files, or binaries) it states. Tests and Pkgs may be nil.
type Run struct {
	At    time.Time
	Secs  float64
	Tests map[string]float64
	Pkgs  map[string]float64
}

// Merge is the sum of runs: the seconds add, a name seen twice takes the
// larger figure, At is the latest.
func Merge(runs ...Run) Run {
	var out Run
	for _, r := range runs {
		out.Secs += r.Secs
		if r.At.After(out.At) {
			out.At = r.At
		}
		out.Tests = maxInto(out.Tests, r.Tests)
		out.Pkgs = maxInto(out.Pkgs, r.Pkgs)
	}
	return out
}

func maxInto(dst, src map[string]float64) map[string]float64 {
	for k, v := range src {
		if dst == nil {
			dst = map[string]float64{}
		}
		if v > dst[k] {
			dst[k] = v
		}
	}
	return dst
}

// Detail is the run as an event's Detail map: "t:<test>" and "p:<package>"
// keys holding seconds, only the slowest MaxTests and MaxPkgs of those at or
// over MinRecordSecs.
func (r Run) Detail() map[string]string {
	out := map[string]string{}
	put := func(prefix string, rows map[string]float64, keep int) {
		for _, id := range slowestIDs(rows, keep) {
			out[prefix+id] = strconv.FormatFloat(math.Round(rows[id]*1000)/1000, 'f', -1, 64)
		}
	}
	put("t:", r.Tests, MaxTests)
	put("p:", r.Pkgs, MaxPkgs)
	return out
}

func slowestIDs(rows map[string]float64, keep int) []string {
	ids := make([]string, 0, len(rows))
	for id, v := range rows {
		if v >= MinRecordSecs {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if rows[ids[i]] != rows[ids[j]] {
			return rows[ids[i]] > rows[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > keep {
		ids = ids[:keep]
	}
	return ids
}

// FromDetail is the inverse of Detail for an event at time at holding secs.
func FromDetail(secs float64, at time.Time, detail map[string]string) Run {
	r := Run{At: at, Secs: secs}
	for k, v := range detail {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(k, "t:"):
			if r.Tests == nil {
				r.Tests = map[string]float64{}
			}
			r.Tests[k[2:]] = f
		case strings.HasPrefix(k, "p:"):
			if r.Pkgs == nil {
				r.Pkgs = map[string]float64{}
			}
			r.Pkgs[k[2:]] = f
		}
	}
	return r
}
