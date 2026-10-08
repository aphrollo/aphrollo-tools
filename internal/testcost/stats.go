package testcost

import (
	"math"
	"sort"
	"strings"
)

// newest is the last n runs by time, oldest first; all of them when n <= 0.
func newest(runs []Run, n int) []Run {
	sorted := append([]Run(nil), runs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	if n > 0 && len(sorted) > n {
		sorted = sorted[len(sorted)-n:]
	}
	return sorted
}

// p50 is the nearest-rank median: deterministic, and a member of the sample.
func p50(vals []float64) float64 {
	sort.Float64s(vals)
	return vals[int(math.Ceil(0.5*float64(len(vals))))-1]
}

// SuiteP50 is the median suite seconds of the newest n runs, rounded up to a
// whole second, and false when fewer than minRuns runs are on record: a
// median over too few runs is not a measurement. One slow run among n moves
// nothing.
func SuiteP50(runs []Run, n, minRuns int) (int, bool) {
	w := newest(runs, n)
	if len(w) == 0 || len(w) < minRuns {
		return 0, false
	}
	secs := make([]float64, len(w))
	for i, r := range w {
		secs[i] = r.Secs
	}
	return int(math.Ceil(p50(secs))), true
}

// OverP50 is the median, over the newest n runs, of how many recorded tests
// ran at or over threshold seconds. False when fewer than minRuns runs exist.
func OverP50(runs []Run, n, minRuns int, threshold float64) (int, bool) {
	w := newest(runs, n)
	if len(w) == 0 || len(w) < minRuns {
		return 0, false
	}
	counts := make([]float64, len(w))
	for i, r := range w {
		for _, s := range r.Tests {
			if s >= threshold {
				counts[i]++
			}
		}
	}
	return int(p50(counts)), true
}

// Slow is one of the slowest tests: its seconds in the newest run that states
// it, the change since the run before that one that does, and whether no
// earlier run states it at all.
type Slow struct {
	ID    string
	Secs  float64
	Delta float64
	New   bool
}

// Slowest is the k slowest tests by their newest figure, slowest first, ties
// by name. A test with a single figure is New and has no Delta.
func Slowest(runs []Run, k int) []Slow {
	latest, prev := map[string]float64{}, map[string]float64{}
	for _, r := range newest(runs, 0) {
		for id, s := range r.Tests {
			if old, seen := latest[id]; seen {
				prev[id] = old
			}
			latest[id] = s
		}
	}
	out := make([]Slow, 0, len(latest))
	for id, s := range latest {
		row := Slow{ID: id, Secs: s}
		if p, ok := prev[id]; ok {
			row.Delta = s - p
		} else {
			row.New = true
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Secs != out[j].Secs {
			return out[i].Secs > out[j].Secs
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// Lookup is the recorded seconds of the test called name (a bare name such as
// TestX or tests::slow), from the newest run that states it. A recorded id
// matches when it ends in the name after a '.', ' ' or "::" separator, or is
// the name.
func Lookup(runs []Run, name string) (float64, bool) {
	w := newest(runs, 0)
	for i := len(w) - 1; i >= 0; i-- {
		for id, s := range w[i].Tests {
			if id == name || strings.HasSuffix(id, "."+name) || strings.HasSuffix(id, " "+name) || strings.HasSuffix(id, "::"+name) {
				return s, true
			}
		}
	}
	return 0, false
}
