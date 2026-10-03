package measure

import (
	"fmt"
	"strings"
)

// textGateLanes is how many lanes the text report lists, the slowest first.
const textGateLanes = 10

// Text is the report as the plain lines `aphrollo stats` prints.
func (r Report) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p("events                 %d", r.Events)
	p("speed (open->merged)   %d lanes  p50 %s  p90 %s", r.Speed.N, secs(r.Speed.P50), secs(r.Speed.P90))
	p("first-run CI green     %d/%d lanes (%.0f%%)", r.CI.Green, r.CI.Lanes, r.CI.Rate)
	p("  red by cause         %s", tally(r.CI.RedByCause))
	for _, o := range r.CI.ByOS {
		p("  os %-18s %d/%d green", o.OS, o.Green, o.Lanes)
	}
	p("gate wall time         %s over %d lanes", secs(r.Gate.TotalSecs), len(r.Gate.Lanes))
	for _, l := range r.Gate.Lanes[:min(len(r.Gate.Lanes), textGateLanes)] {
		p("  %-20s %s", laneName(l.Lane), secs(l.Secs))
	}
	if more := len(r.Gate.Lanes) - textGateLanes; more > 0 {
		p("  ... %d more lanes (--json lists them all)", more)
	}
	if r.Gate.Implausible > 0 {
		p("  left out             %d timing events with a negative or over-24h duration", r.Gate.Implausible)
	}
	p("not tested             %d/%d runs (%.0f%%)", r.Runs.NotTested, r.Runs.Total, r.Runs.NotTestedShare*100)
	p("  by cause             %s", tally(r.Runs.Causes))
	p("edit->verdict          %d runs  p50 %.0fms  p95 %.0fms", r.Runs.Latency.N, r.Runs.Latency.P50, r.Runs.Latency.P95)
	p("edits per message      %d edits / %d messages (mean %.2f, max %d)", r.Edits.Edits, r.Edits.Messages, r.Edits.Mean, r.Edits.Max)
	p("denies                 %d  by rule: %s", r.Denies.Denies, tally(r.Denies.ByRule))
	p("  by cause             %s", tally(r.Denies.ByCause))
	p("overrides              %d  %s", r.Denies.Overrides, tally(r.Denies.ByOverride))
	p("wrong blocks           %d (override within %.0f min of a deny)", r.Denies.WrongBlocks, WrongBlockWindow.Minutes())
	p("escapes by class       %s  (false positives %d)", tally(r.Escapes.ByClass), r.Escapes.FalsePositives)
	return b.String()
}

func secs(s float64) string {
	switch {
	case s >= 3600:
		return fmt.Sprintf("%.1fh", s/3600)
	case s >= 60:
		return fmt.Sprintf("%.1fm", s/60)
	}
	return fmt.Sprintf("%.1fs", s)
}

func tally(cs []Count) string {
	if len(cs) == 0 {
		return "none"
	}
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%s %d", c.Name, c.N)
	}
	return strings.Join(parts, ", ")
}

func laneName(l string) string {
	if l == "" {
		return "(no lane)"
	}
	return l
}
