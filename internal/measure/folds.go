package measure

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// WrongBlockWindow is how soon after a deny an override on the same lane makes
// the deny a wrong block.
const WrongBlockWindow = 10 * time.Minute

// foldSpeed is lane opened to merged. The v1 log has no lane.opened event, so a
// lane opens at its first event, and again at the first event after each merge
// so a reused lane name is two lanes. A merge made outside the verb has no
// opening and is skipped.
func foldSpeed(s scope) Dist {
	opened := map[string]time.Time{}
	var secs []float64
	for _, e := range s.evs {
		if e.Lane == "" || e.Detail["by"] == "outside" {
			continue
		}
		// A merge the verb only queued has not landed: the ok event written once the
		// queue has merged it closes the lane.
		if e.Kind != "merge" || e.Verdict == "queued" {
			if _, ok := opened[e.Lane]; !ok {
				opened[e.Lane] = e.at
			}
			continue
		}
		if start, ok := opened[e.Lane]; ok && s.in(e.at) {
			secs = append(secs, e.at.Sub(start).Seconds())
		}
		delete(opened, e.Lane)
	}
	return dist(secs)
}

// CI is the first settled CI result of each lane. Rate is the green share in
// percent.
type CI struct {
	Lanes      int      `json:"lanes"`
	Green      int      `json:"green"`
	Rate       float64  `json:"rate_pct"`
	RedByCause []Count  `json:"red_by_cause"`
	ByOS       []OSRate `json:"by_os"`
}

// OSRate is the first-run result of the lanes whose ci event named an OS.
type OSRate struct {
	OS    string `json:"os"`
	Lanes int    `json:"lanes"`
	Green int    `json:"green"`
}

// foldCI reads the first ci event of each lane. A ci event carries no OS today,
// so the OS split reads detail "os" and files the rest under "unknown".
func foldCI(s scope) CI {
	// counted is a lane's first result as tallied: a green one can still turn red
	// when the merge queue's own run of the PR fails.
	type counted struct {
		os    string
		green bool
	}
	seen := map[string]*counted{}
	causes := map[string]int{}
	type tally struct{ lanes, green int }
	byOS := map[string]*tally{}
	var c CI
	redCause := func(e stamped) string {
		if cause := e.Detail["cause"]; cause != "" {
			return cause
		}
		return "other"
	}
	for _, e := range s.evs {
		if e.Kind != "ci" || e.Lane == "" || (e.Verdict != "green" && e.Verdict != "red") {
			continue
		}
		if prior, ok := seen[e.Lane]; ok {
			// A merge queue red is a red of the lane whatever its PR run said: the
			// PR was green, the queue refused it all the same.
			if prior != nil && prior.green && e.Verdict == "red" && e.Detail["ci"] == "queue" {
				prior.green = false
				c.Green--
				byOS[prior.os].green--
				causes[redCause(e)]++
			}
			continue
		}
		if !s.in(e.at) {
			seen[e.Lane] = nil
			continue
		}
		osName := e.Detail["os"]
		if osName == "" {
			osName = "unknown"
		}
		if byOS[osName] == nil {
			byOS[osName] = &tally{}
		}
		c.Lanes++
		byOS[osName].lanes++
		seen[e.Lane] = &counted{os: osName, green: e.Verdict == "green"}
		if e.Verdict == "green" {
			c.Green++
			byOS[osName].green++
			continue
		}
		causes[redCause(e)]++
	}
	c.Rate = share(c.Green, c.Lanes) * 100
	c.RedByCause = counts(causes)
	for name, t := range byOS {
		c.ByOS = append(c.ByOS, OSRate{name, t.lanes, t.green})
	}
	sort.Slice(c.ByOS, func(i, j int) bool { return c.ByOS[i].OS < c.ByOS[j].OS })
	return c
}

// maxPlausibleSecs is the longest one timed stage or hook can have taken.
const maxPlausibleSecs = 24 * 3600

// Gate is the gate's wall time: stage.timing plus hook.timing seconds, per lane.
// Implausible counts the timing events left out of the sum because their
// duration is negative or over a day: a clock or overflow fault, not a
// measurement.
type Gate struct {
	TotalSecs   float64    `json:"total_secs"`
	Lanes       []LaneSecs `json:"lanes"`
	Implausible int        `json:"implausible_events"`
}

// LaneSecs is one lane's gate seconds.
type LaneSecs struct {
	Lane string  `json:"lane"`
	Secs float64 `json:"secs"`
}

func foldGate(s scope) Gate {
	per := map[string]float64{}
	var g Gate
	for _, e := range s.evs {
		if (e.Kind != "stage.timing" && e.Kind != "hook.timing") || !s.in(e.at) {
			continue
		}
		if e.Secs < 0 || e.Secs > maxPlausibleSecs {
			g.Implausible++
			continue
		}
		per[e.Lane] += e.Secs
		g.TotalSecs += e.Secs
	}
	for lane, secs := range per {
		g.Lanes = append(g.Lanes, LaneSecs{lane, secs})
	}
	sort.Slice(g.Lanes, func(i, j int) bool {
		if g.Lanes[i].Secs != g.Lanes[j].Secs {
			return g.Lanes[i].Secs > g.Lanes[j].Secs
		}
		return g.Lanes[i].Lane < g.Lanes[j].Lane
	})
	return g
}

// Runs counts run.result events, how many proved nothing and why, and how long
// after an edit its verdict arrived (Latency, in milliseconds).
type Runs struct {
	Total          int     `json:"total"`
	NotTested      int     `json:"not_tested"`
	NotTestedShare float64 `json:"not_tested_share"`
	Causes         []Count `json:"not_tested_by_cause"`
	Latency        Dist    `json:"edit_to_verdict_ms"`
}

func foldRuns(s scope) Runs {
	causes := map[string]int{}
	var r Runs
	var latency []float64
	for _, e := range s.evs {
		if e.Kind != "run.result" || !s.in(e.at) {
			continue
		}
		r.Total++
		if e.Detail["result"] == "not-tested" {
			r.NotTested++
			causes[e.Detail["cause"]]++
		}
		if ms, err := strconv.ParseFloat(e.Detail["latency_ms"], 64); err == nil {
			latency = append(latency, ms)
		}
	}
	r.NotTestedShare = share(r.NotTested, r.Total)
	r.Causes = counts(causes)
	r.Latency = dist(latency)
	return r
}

// Edits is the number of edit events between a session's userpromptsubmit
// hooks. A message is the span from one boundary to the next (or to the end of
// the log); an edit before a session's first boundary belongs to no message.
type Edits struct {
	Messages int     `json:"messages"`
	Edits    int     `json:"edits"`
	Mean     float64 `json:"mean"`
	Max      int     `json:"max"`
}

func foldEdits(s scope) Edits {
	open := map[string]int{} // session -> edits in its current message
	var e Edits
	for _, x := range s.evs {
		if !s.in(x.at) {
			continue
		}
		session, _, _ := strings.Cut(x.Actor, "/")
		switch {
		case x.Kind == "hook.timing" && x.Detail["hook"] == "userpromptsubmit":
			if prev, ok := open[session]; ok {
				e.Max = max(e.Max, prev)
			}
			open[session] = 0
			e.Messages++
		case x.Kind == "edit":
			if _, ok := open[session]; ok {
				open[session]++
				e.Edits++
				e.Max = max(e.Max, open[session])
			}
		}
	}
	e.Mean = share(e.Edits, e.Messages)
	return e
}

// Denies counts refusals by rule and cause, the overrides that waived one, and
// the wrong blocks: an override within WrongBlockWindow after a deny on the
// same lane.
type Denies struct {
	Denies      int     `json:"denies"`
	ByRule      []Count `json:"by_rule"`
	ByCause     []Count `json:"by_cause"`
	Overrides   int     `json:"overrides"`
	ByOverride  []Count `json:"by_override"`
	WrongBlocks int     `json:"wrong_blocks"`
}

// denyRule is the rule a deny came from: its detail, else its verdict (the
// older writer put the rule there, after "pretooluse-denied:").
func denyRule(e stamped) string {
	if rule := e.Detail["rule"]; rule != "" {
		return rule
	}
	if rule := strings.TrimPrefix(e.Verdict, "pretooluse-denied:"); rule != "" {
		return rule
	}
	return "unknown"
}

// isAllowedRerun is an override event of the old spelling of an allowed narrowed
// rerun (now logged as rerun-bash-narrowed, which is no override at all): the
// hook let the run through, so nothing was waived and no block was wrong.
func isAllowedRerun(e stamped) bool {
	return e.Detail["override"] == "override-bash-narrowed" || e.Verdict == "override-bash-narrowed"
}

func foldDenies(s scope) Denies {
	rules, causes, overrides := map[string]int{}, map[string]int{}, map[string]int{}
	lastDeny := map[string]time.Time{}
	var d Denies
	for _, e := range s.evs {
		switch e.Kind {
		case "deny":
			lastDeny[e.Lane] = e.at
			if !s.in(e.at) {
				continue
			}
			d.Denies++
			rules[denyRule(e)]++
			if cause := e.Detail["cause"]; cause != "" {
				causes[cause]++
			}
		case "override":
			if !s.in(e.at) || isAllowedRerun(e) {
				continue
			}
			d.Overrides++
			overrides[e.Detail["override"]]++
			// A deny is one wrong block however many overrides follow it.
			if t, ok := lastDeny[e.Lane]; ok && e.at.Sub(t) <= WrongBlockWindow {
				d.WrongBlocks++
				delete(lastDeny, e.Lane)
			}
		}
	}
	d.ByRule, d.ByCause, d.ByOverride = counts(rules), counts(causes), counts(overrides)
	return d
}

// Escapes counts escape events by class; a false positive is a wrong deny, not
// an escape, and is counted apart. An outside merge is its own class.
type Escapes struct {
	ByClass        []Count `json:"by_class"`
	FalsePositives int     `json:"false_positives"`
}

func foldEscapes(s scope) Escapes {
	classes := map[string]int{}
	var x Escapes
	for _, e := range s.evs {
		if e.Kind != "escape" || !s.in(e.at) {
			continue
		}
		switch e.Verdict {
		case "false-positive":
			x.FalsePositives++
		case "outside-merge":
			classes["outside-merge"]++
		default:
			class := e.Detail["class"]
			if class == "" {
				class = "unclassified"
			}
			classes[class]++
		}
	}
	x.ByClass = counts(classes)
	return x
}
