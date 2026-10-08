package report

import (
	"fmt"
	"math"
	"slices"
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
	// Clear is whether the change passes the benchstat test: four runs on each side and a Mann-Whitney p under 0.05; P is that p, 0 when the test was not run. Only a clear change is shown.
	Clear bool    `json:"clear_change"`
	P     float64 `json:"p,omitempty"`
	// Versions are the binary versions the runs of the window span, in order of first appearance, each against the one before; empty unless there are two or more.
	Versions []SpeedVersion `json:"versions,omitempty"`
}

// SpeedVersion is a stage over the runs a binary version wrote. A version with
// fewer than minClearRuns runs is merged into the version before it, and the label says so.
type SpeedVersion struct {
	Label   string   `json:"label"`
	N       int      `json:"n"`
	P50     float64  `json:"p50_secs"`
	P90     float64  `json:"p90_secs"`
	Max     float64  `json:"max_secs"`
	PrevP50 float64  `json:"prev_p50_secs,omitempty"`
	Change  *float64 `json:"p50_change_secs,omitempty"`
	Clear   bool     `json:"clear_change"`
	P       float64  `json:"p,omitempty"`
}

// Speed is the gate's duration trend: whether it is getting faster.
type Speed struct {
	Rows []SpeedRow `json:"rows"`
	// Gaps are the durations the log cannot give yet, each with the smallest event change that would.
	Gaps []string `json:"gaps,omitempty"`
	// Dropped counts the samples longer than maxSpeedSecs: a duration no run can
	// have (a clock read against a zero start), counted so none is dropped unseen.
	Dropped int `json:"dropped,omitempty"`
}

// maxSpeedSecs is the longest duration a sample may record: 30 days, longer than
// any gate run, CI run or PR the report reads.
const maxSpeedSecs = 30 * 24 * 3600

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
	{name: "commit gate total"},
	{name: "merge gate"},
	{name: "merge gate total"},
	{name: "mutation (commit)", gap: "mutation at commit: no mutation run in the window carries its seconds (recorded from v1.34.0 on)"},
	{name: "merge queue", gap: "merge queue: no PR in the window has both its enqueue and its merge recorded"},
	{name: "CI pipeline", gap: "CI pipeline: no ci event in the window carries its run's seconds"},
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
	ver  string
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
		key = class + ": " + gateStage(e.Verdict, e.Cmd)
	}
	return speedSample{key: key, at: e.at, secs: secs, ver: e.BinVer}, true
}

// gateVerdictStages names the gate stage a verdict prefix belongs to, where the
// command alone does not tell it: every guard and the fail-first run are a go test.
var gateVerdictStages = map[string]string{
	"red": "fail-first", "green": "fail-first", "still": "fail-first",
	"tddsplit": "tddsplit", "callsites": "callsites", "lint": "lint", "vet": "vet",
	"docs": "docs", "mechanical": "mechanical", "ratchet": "ratchet check", "mutants": "mutants",
}

// gateStage is the row a gate's stage line belongs to: the stage its verdict
// names, else the tool its command runs ("go test", "golangci-lint"), so the
// rows are a gate's stages and not one per command line.
func gateStage(verdict, cmd string) string {
	prefix, _, _ := strings.Cut(verdict, "-")
	prefix, _, _ = strings.Cut(prefix, ":")
	// A bare green or red is any stage's verdict; only red-proven and
	// green-proven name the fail-first run.
	plain := (prefix == "green" || prefix == "red") && !strings.HasSuffix(verdict, "-proven")
	if s, ok := gateVerdictStages[prefix]; ok && !plain {
		return s
	}
	f := strings.Fields(cmd)
	switch {
	case len(f) == 0:
		return "(unnamed)"
	case f[0] == "go" && len(f) > 1:
		return "go " + f[1]
	}
	return f[0]
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
		case e.Kind == "merge" && (e.Verdict == "queued" || e.Verdict == "enqueued"):
			queued[pr] = e.at // the last enqueue is the one the merge waited on
		case e.Kind == "merge" && e.Verdict == "ok" && !done[pr]:
			done[pr] = true
			if t, ok := opened[pr]; ok && e.at.After(t) {
				out = append(out, speedSample{"PR lead time", e.at, e.at.Sub(t).Seconds(), e.BinVer})
			}
			if t, ok := queued[pr]; ok && e.at.After(t) && e.Detail["method"] == "merge queue" {
				out = append(out, speedSample{"merge queue", e.at, e.at.Sub(t).Seconds(), e.BinVer})
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
	curSamples := map[string][]speedSample{}
	dropped := 0
	for _, s := range all {
		switch {
		case s.secs > maxSpeedSecs:
			if since.IsZero() || !s.at.Before(since) {
				dropped++
			}
		case since.IsZero() || !s.at.Before(since):
			cur[s.key] = append(cur[s.key], s.secs)
			curSamples[s.key] = append(curSamples[s.key], s)
		case hasPrev && !s.at.Before(from):
			prev[s.key] = append(prev[s.key], s.secs)
		}
	}
	firstSeen := versionDates(evs, since)
	var sp Speed
	for k, v := range cur {
		sort.Float64s(v)
		row := SpeedRow{Stage: k, N: len(v), P50: percentile(v, 50), P90: percentile(v, 90), Max: v[len(v)-1]}
		if p := prev[k]; len(p) > 0 {
			sort.Float64s(p)
			row.PrevN, row.PrevP50 = len(p), percentile(p, 50)
			c := row.P50 - row.PrevP50
			row.Change = &c
			row.Clear, row.P = clearOf(v, p)
		}
		row.Versions = versionColumns(curSamples[k], firstSeen)
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
	if dropped > 0 {
		sp.Dropped = dropped
		noun := "durations"
		if dropped == 1 {
			noun = "duration"
		}
		sp.Gaps = append(sp.Gaps, fmt.Sprintf("%d %s longer than 30 days left out: no run lasts that long, so the start it was measured from was never recorded", dropped, noun))
	}
	return sp
}

// text writes the compact block of the text report.
func (s Speed) text(p func(string, ...any), more func(int, int)) {
	p("Speed (seconds per run, green or not; change is the p50 against the window before, + is slower, ~ is no clear change)")
	if len(s.Rows) == 0 {
		p("  no runs timed in the window")
	}
	for _, r := range s.Rows[:min(len(s.Rows), textRows)] {
		p("%s", strings.TrimRight(fmt.Sprintf("  %-44.44s n %-4d p50 %-7s p90 %-7s max %-7s %s", r.Stage, r.N, secsText(r.P50), secsText(r.P90), secsText(r.Max), r.changeText()), " "))
		for _, v := range r.Versions {
			p("%s", strings.TrimRight(fmt.Sprintf("    %-42.42s n %-4d p50 %-7s p90 %-7s max %-7s %s", v.Label, v.N, secsText(v.P50), secsText(v.P90), secsText(v.Max), changeText(v.Change, v.Clear)), " "))
		}
	}
	more(textRows, len(s.Rows))
	for _, g := range s.Gaps {
		p("  not derivable: %s", g)
	}
}

func (r SpeedRow) changeText() string { return changeText(r.Change, r.Clear) }

// changeText shows a change only when it is clear; an unclear one reads ~, as in benchstat.
func changeText(change *float64, clear bool) string {
	switch {
	case change == nil:
		return ""
	case !clear, *change == 0:
		return "change ~"
	}
	return "change " + signedSecs(*change)
}

func secsText(s float64) string {
	if r := math.Round(s*10) / 10; r < 10 {
		return strconv.FormatFloat(r, 'f', -1, 64) + "s"
	}
	return dur(s)
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

// clearOf is whether the change from prev to cur is clear and the p value the
// test gave, 0 when a side is too small to test.
func clearOf(cur, prev []float64) (clear bool, p float64) {
	if len(cur) < minClearRuns || len(prev) < minClearRuns {
		return false, 0
	}
	p, clear = clearChange(cur, prev)
	return clear, p
}

// versionDates is the day each binary version first wrote an event of the window.
func versionDates(evs []stamped, since time.Time) map[string]time.Time {
	first := map[string]time.Time{}
	for _, e := range evs {
		if e.BinVer == "" || (!since.IsZero() && e.at.Before(since)) {
			continue
		}
		if t, ok := first[e.BinVer]; !ok || e.at.Before(t) {
			first[e.BinVer] = e.at
		}
	}
	return first
}

func versionName(v string) string { return "v" + strings.TrimPrefix(v, "v") }

// versionColumns splits the runs of one stage by the binary version that wrote
// them, in order of first appearance. A version with fewer than minClearRuns
// runs merges into the version before it. Each column is compared with the one
// before it by the same test as the week before. Fewer than two columns is no
// split at all.
func versionColumns(samples []speedSample, first map[string]time.Time) []SpeedVersion {
	byVer := map[string][]float64{}
	for _, s := range samples {
		if s.ver != "" {
			byVer[s.ver] = append(byVer[s.ver], s.secs)
		}
	}
	vers := make([]string, 0, len(byVer))
	for v := range byVer {
		vers = append(vers, v)
	}
	sort.Slice(vers, func(i, j int) bool {
		if !first[vers[i]].Equal(first[vers[j]]) {
			return first[vers[i]].Before(first[vers[j]])
		}
		return vers[i] < vers[j]
	})
	type group struct {
		vers []string
		secs []float64
	}
	var groups []group
	for _, v := range vers {
		if n := len(groups); n > 0 && len(byVer[v]) < minClearRuns {
			groups[n-1].vers = append(groups[n-1].vers, v)
			groups[n-1].secs = append(groups[n-1].secs, byVer[v]...)
			continue
		}
		groups = append(groups, group{[]string{v}, slices.Clone(byVer[v])})
	}
	if len(groups) < 2 {
		return nil
	}
	var out []SpeedVersion
	var before []float64
	for _, g := range groups {
		sort.Float64s(g.secs)
		label := "since " + versionName(g.vers[0]) + ", " + first[g.vers[0]].UTC().Format("2006-01-02")
		if len(g.vers) > 1 {
			names := make([]string, len(g.vers)-1)
			for i, v := range g.vers[1:] {
				names[i] = versionName(v)
			}
			label += " (merged with " + strings.Join(names, ", ") + ")"
		}
		col := SpeedVersion{Label: label, N: len(g.secs), P50: percentile(g.secs, 50), P90: percentile(g.secs, 90), Max: g.secs[len(g.secs)-1]}
		if before != nil {
			col.PrevP50 = percentile(before, 50)
			c := col.P50 - col.PrevP50
			col.Change = &c
			col.Clear, col.P = clearOf(g.secs, before)
		}
		before = g.secs
		out = append(out, col)
	}
	return out
}
