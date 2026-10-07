package report

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SpeedRow is how long one stage took over the window: every run of it, green or
// not, as exact nearest-rank percentiles in seconds. Change is the p50 against
// the window before (positive is slower), nil when either window has no run.
type SpeedRow struct {
	Stage   string   `json:"stage"`
	N       int      `json:"n"`
	P50     float64  `json:"p50_secs"`
	P90     float64  `json:"p90_secs"`
	Max     float64  `json:"max_secs"`
	PrevN   int      `json:"prev_n"`
	PrevP50 float64  `json:"prev_p50_secs"`
	Change  *float64 `json:"p50_change_secs,omitempty"`
}

// Speed is the gate's duration trend: whether it is getting faster.
type Speed struct {
	Rows []SpeedRow `json:"rows"`
	// Gaps are the durations the log cannot give yet, each with the smallest event change that would.
	Gaps []string `json:"gaps,omitempty"`
}

// speedClass is a duration the report reads. Classes are data: a new one is a
// row here and a case in speedOf, never a new fold.
type speedClass struct {
	name string
	// gap says what the log lacks and the smallest change that adds it; "" for a class the log always carries.
	gap string
}

var speedClasses = []speedClass{
	{name: "edit suite"},
	{name: "edit to verdict"},
	{name: "commit gate"},
	{name: "commit gate total", gap: "commit gate total: commit_gate_result carries no secs; stamp the run's elapsed seconds on it in internal/cli/gatehooks.go"},
	{name: "merge gate"},
	{name: "merge gate total", gap: "merge gate total: merge_gate_result carries no secs; stamp the run's elapsed seconds on it in internal/cli/gatehooks.go"},
	{name: "mutation (commit)", gap: "mutation at commit: the mutants stage line is written with 0 seconds; pass the measured duration at the AppendGateLog call in internal/tdd/mutation/mutants_measure_judge.go"},
	{name: "merge queue"},
	{name: "CI pipeline", gap: "CI pipeline: a ci event carries no run duration (its time is when the result was read, or the run's creation); add detail secs (completed minus created) in internal/workspace/cievent.go and firstrun.go"},
	{name: "PR lead time"},
}

// speedByKind is the class of a kind that names its stage; speedByStage the
// class of a kind that only carries one (a timing line, a run's result).
var (
	speedByKind = map[string]string{
		"commit_gate": "commit gate", "merge_gate": "merge gate",
		"commit_gate_result": "commit gate total", "merge_gate_result": "merge gate total",
		"mutants": "mutation (commit)",
	}
	speedByStage = map[string]string{
		"postedit": "edit suite", "precommit": "commit gate", "premerge": "merge gate",
		"premergecommit": "merge gate", "mutants": "mutation (commit)",
	}
	// speedCmdClasses keep one row per command: a gate's stages are its commands.
	speedCmdClasses = map[string]bool{"commit gate": true, "merge gate": true}
)

type speedSample struct {
	key  string
	at   time.Time
	secs float64
}

// floatOf is the number in s, with ok false when s holds none.
func floatOf(s string) (v float64, ok bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// speedOf is the duration one event records and the row it belongs to.
func speedOf(e stamped) (speedSample, bool) {
	class, known := speedByKind[e.Kind]
	if !known && (e.Kind == "stage.timing" || e.Kind == "run.result") {
		class, known = speedByStage[e.Stage]
	}
	secs := e.Secs
	switch {
	case e.Kind == "run.result" && e.Stage == "":
		ms, ok := floatOf(e.Detail["latency_ms"])
		class, known, secs = "edit to verdict", ok, ms/1000
	case e.Kind == "ci":
		s, ok := floatOf(e.Detail["secs"])
		class, known, secs = "CI pipeline", ok, s
	case e.Kind == "mutants" && strings.Contains(e.Verdict, "ci-busy"):
		// A wait for CI to finish, not a mutation run.
		known = false
	}
	if !known || secs <= 0 {
		return speedSample{}, false
	}
	key := class
	if speedCmdClasses[class] {
		cmd := e.Cmd
		if cmd == "" {
			cmd = "(unnamed)"
		}
		key = class + ": " + cmd
	}
	return speedSample{key: key, at: e.at, secs: secs}, true
}

// pairedSpeed is the durations that span two events of a PR: open to merge, and
// enqueue to merge. A pair is stamped with its end; a PR with one end only has no
// duration, and none is guessed.
func pairedSpeed(evs []stamped) []speedSample {
	opened := map[string]time.Time{}
	queued := map[string]time.Time{}
	done := map[string]bool{}
	var out []speedSample
	for _, e := range evs {
		pr := e.Detail["pr"]
		if pr == "" {
			continue
		}
		switch {
		case e.Kind == "pr_opened":
			if _, ok := opened[pr]; !ok {
				opened[pr] = e.at
			}
		case e.Kind == "merge" && e.Verdict == "queued":
			if _, ok := queued[pr]; !ok {
				queued[pr] = e.at
			}
		case e.Kind == "merge" && e.Verdict == "ok" && !done[pr]:
			done[pr] = true
			if t, ok := opened[pr]; ok {
				out = append(out, speedSample{"PR lead time", e.at, e.at.Sub(t).Seconds()})
			}
			if t, ok := queued[pr]; ok && e.Detail["method"] == "merge queue" {
				out = append(out, speedSample{"merge queue", e.at, e.at.Sub(t).Seconds()})
			}
		}
	}
	return out
}

// percentile is the nearest-rank p-th percentile of sorted, which must not be empty.
func percentile(sorted []float64, p int) float64 {
	rank := int(math.Ceil(float64(p) / 100 * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}

// classOf is the index in speedClasses of the class a row key belongs to.
func classOf(key string) int {
	for i, c := range speedClasses {
		if key == c.name || strings.HasPrefix(key, c.name+": ") {
			return i
		}
	}
	return len(speedClasses)
}

// buildSpeed reads the window from since (the whole log when zero) against the
// window [from, since) before it when hasPrev.
func buildSpeed(evs []stamped, since, from time.Time, hasPrev bool) Speed {
	all := pairedSpeed(evs)
	for _, e := range evs {
		if s, ok := speedOf(e); ok {
			all = append(all, s)
		}
	}
	cur, prev := map[string][]float64{}, map[string][]float64{}
	for _, s := range all {
		switch {
		case since.IsZero() || !s.at.Before(since):
			cur[s.key] = append(cur[s.key], s.secs)
		case hasPrev && !s.at.Before(from):
			prev[s.key] = append(prev[s.key], s.secs)
		}
	}
	var sp Speed
	for k, v := range cur {
		sort.Float64s(v)
		row := SpeedRow{Stage: k, N: len(v), P50: percentile(v, 50), P90: percentile(v, 90), Max: v[len(v)-1]}
		if p := prev[k]; len(p) > 0 {
			sort.Float64s(p)
			row.PrevN, row.PrevP50 = len(p), percentile(p, 50)
			c := row.P50 - row.PrevP50
			row.Change = &c
		}
		sp.Rows = append(sp.Rows, row)
	}
	sort.Slice(sp.Rows, func(i, j int) bool {
		a, b := sp.Rows[i].Stage, sp.Rows[j].Stage
		if oa, ob := classOf(a), classOf(b); oa != ob {
			return oa < ob
		}
		return a < b
	})
	seen := map[int]bool{}
	for k := range cur {
		seen[classOf(k)] = true
	}
	for k := range prev {
		seen[classOf(k)] = true
	}
	for i, c := range speedClasses {
		if c.gap != "" && !seen[i] {
			sp.Gaps = append(sp.Gaps, c.gap)
		}
	}
	return sp
}

// text writes the compact block of the text report.
func (s Speed) text(p func(string, ...any), more func(int, int)) {
	p("Speed (seconds per run, green or not; change is the p50 against the window before, + is slower)")
	if len(s.Rows) == 0 {
		p("  no runs timed in the window")
	}
	for _, r := range s.Rows[:min(len(s.Rows), textRows)] {
		p("%s", strings.TrimRight(fmt.Sprintf("  %-44.44s n %-4d p50 %-7s p90 %-7s max %-7s %s", r.Stage, r.N, secsText(r.P50), secsText(r.P90), secsText(r.Max), r.changeText()), " "))
	}
	more(textRows, len(s.Rows))
	for _, g := range s.Gaps {
		p("  not derivable: %s", g)
	}
}

func (r SpeedRow) changeText() string {
	if r.Change == nil {
		return ""
	}
	return "change " + signedSecs(*r.Change)
}

func secsText(s float64) string {
	if s >= 10 {
		return dur(s)
	}
	return strconv.FormatFloat(s, 'f', -1, 64) + "s"
}

func signedSecs(s float64) string {
	sign := "+"
	if s < 0 {
		sign, s = "-", -s
	}
	if s == 0 {
		return "0s"
	}
	return sign + secsText(s)
}
