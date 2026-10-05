// Package measure folds the v1 event log into the measures the pipeline is
// judged by. Every function is pure: the events and the clock come in, a
// Report goes out, and nothing is read or written. Where the log cannot answer
// a measure yet (no lane.opened event, no OS on a ci event) the fold says what
// it used instead.
package measure

import (
	"math"
	"sort"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Options narrows a Report. Lane keeps the events of one lane; Window keeps the
// events of the last Window before now (0 keeps all). A measure that spans a
// lane's life (speed, first-run CI) reads the whole log to find the lane's
// start, and the window decides only whether the lane's merge or first result
// falls inside it.
type Options struct {
	Lane   string
	Window time.Duration
}

// Report is every measure the v1 events can answer today.
type Report struct {
	Events    int       `json:"events"`
	Speed     Dist      `json:"speed_secs"`
	CI        CI        `json:"ci_first_run"`
	Gate      Gate      `json:"gate_wall"`
	Runs      Runs      `json:"runs"`
	Edits     Edits     `json:"edits_per_message"`
	Denies    Denies    `json:"denies"`
	Escapes   Escapes   `json:"escapes"`
	LawMisses LawMisses `json:"law_misses"`
}

// Dist is a count and the nearest-rank percentiles of a sample.
type Dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
}

// Count is one named tally.
type Count struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

// stamped is an event with its time parsed.
type stamped struct {
	tdd.Event
	at time.Time
}

// scope is the lane's events in time order plus the window test.
type scope struct {
	evs   []stamped
	since time.Time
}

func (s scope) in(at time.Time) bool { return s.since.IsZero() || !at.Before(s.since) }

// Compute folds events as of now. The result depends only on the events and
// now, never on their order in the slice.
func Compute(events []tdd.Event, now time.Time, o Options) Report {
	s := newScope(events, now, o)
	r := Report{Speed: foldSpeed(s), CI: foldCI(s), Gate: foldGate(s), Runs: foldRuns(s),
		Edits: foldEdits(s), Denies: foldDenies(s), Escapes: foldEscapes(s), LawMisses: foldLawMisses(s, o.Window)}
	for _, e := range s.evs {
		if s.in(e.at) {
			r.Events++
		}
	}
	return r
}

// newScope is the events of the lane in time order, with the window's start.
func newScope(events []tdd.Event, now time.Time, o Options) scope {
	s := scope{}
	if o.Window > 0 {
		s.since = now.Add(-o.Window)
	}
	for _, e := range events {
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil || (o.Lane != "" && e.Lane != o.Lane) {
			continue
		}
		s.evs = append(s.evs, stamped{e, at})
	}
	sort.SliceStable(s.evs, func(i, j int) bool { return s.evs[i].at.Before(s.evs[j].at) })
	return s
}

// percentile is the nearest-rank p-quantile of the ascending sample, 0 for none.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(max(rank, 0), len(sorted)-1)]
}

func dist(sample []float64) Dist {
	sort.Float64s(sample)
	return Dist{N: len(sample), P50: percentile(sample, 0.5), P90: percentile(sample, 0.9), P95: percentile(sample, 0.95)}
}

// counts turns a tally into a slice, largest first, ties by name.
func counts(m map[string]int) []Count {
	var out []Count
	for name, n := range m {
		out = append(out, Count{name, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func share(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}
