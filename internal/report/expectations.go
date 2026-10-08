package report

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/expect"
)

// expectMinLanes is how many lanes must have run on a newer binary than the
// release a PR followed before its expectation is read.
const expectMinLanes = 30

// The states an expectation is in.
const (
	expectPending  = "pending"
	expectHeld     = "held"
	expectNotHeld  = "not held"
	expectUnproven = "unproven"
)

// ExpectationRow is one `expect:` line of a merged PR, read against the log.
type ExpectationRow struct {
	PR          string  `json:"pr"`
	Expect      string  `json:"expect"`
	After       string  `json:"after_release"`
	Lanes       int     `json:"lanes"`
	State       string  `json:"state"`
	Before      float64 `json:"before,omitempty"`
	AfterValue  float64 `json:"after,omitempty"`
	BeforeN     int     `json:"before_n,omitempty"`
	AfterN      int     `json:"after_n,omitempty"`
	Clear       bool    `json:"clear_change,omitempty"`
	P           float64 `json:"p,omitempty"`
	Rate        bool    `json:"rate,omitempty"`
	Description string  `json:"text"`
}

// Expectations are the recorded expectations of the merged PRs.
type Expectations struct {
	Rows []ExpectationRow `json:"rows"`
}

type expectRecord struct {
	pr    string
	e     expect.Expectation
	after string
}

// expectRecords are the expectations merge events recorded, each PR's line once
// (a queued merge and its landing both carry it), in event order.
func expectRecords(evs []stamped) []expectRecord {
	var out []expectRecord
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Kind != "merge" || ev.Detail["expect"] == "" {
			continue
		}
		pr := ev.Detail["pr"]
		if pr == "" {
			pr = "seq" + strconv.FormatInt(ev.Seq, 10)
		}
		for _, e := range expect.Decode(ev.Detail["expect"]) {
			if k := pr + " " + e.String(); !seen[k] {
				seen[k] = true
				out = append(out, expectRecord{pr, e, ev.Detail["after"]})
			}
		}
	}
	return out
}

func releaseOf(s string) (compat.Version, bool) {
	v, err := compat.ParseVersion(strings.TrimPrefix(s, "v"))
	return v, err == nil
}

// buildExpectations reads every recorded expectation against the whole log: the
// runs of binaries up to the release the PR followed are before, the runs of
// newer binaries after. It needs expectMinLanes lanes of newer events to read.
func buildExpectations(evs []stamped) Expectations {
	recs := expectRecords(evs)
	var out Expectations
	if len(recs) == 0 {
		return out
	}
	samples := pairedSpeed(evs)
	for _, e := range evs {
		if s, ok := speedOf(e); ok {
			samples = append(samples, s)
		}
	}
	for _, rec := range recs {
		out.Rows = append(out.Rows, readExpectation(rec, evs, samples))
	}
	return out
}

func readExpectation(rec expectRecord, evs []stamped, samples []speedSample) ExpectationRow {
	row := ExpectationRow{PR: rec.pr, Expect: rec.e.String(), After: rec.after}
	floor, _ := releaseOf(rec.after) // no release yet: every versioned run is after
	newer := func(ver string) (isAfter, known bool) {
		v, ok := releaseOf(ver)
		return floor.Less(v), ok
	}
	lanes := map[string]bool{}
	for _, e := range evs {
		if isAfter, known := newer(e.BinVer); known && isAfter && e.Lane != "" {
			lanes[e.Lane] = true
		}
	}
	row.Lanes = len(lanes)
	if row.Lanes < expectMinLanes {
		row.State = expectPending
		row.Description = fmt.Sprintf("pending (%d of %d lanes)", row.Lanes, expectMinLanes)
		return row
	}
	m, _ := expect.Find(rec.e.Metric)
	var before, after []float64
	if m.Source == expect.SourceWrongBlocks {
		return readRate(row, rec.e, evs, newer)
	}
	for _, s := range samples {
		if i := classOf(s.key); i >= len(speedClasses) || speedClasses[i].name != m.Source {
			continue
		}
		if isAfter, known := newer(s.ver); known {
			if isAfter {
				after = append(after, s.secs)
			} else {
				before = append(before, s.secs)
			}
		}
	}
	sort.Float64s(before)
	sort.Float64s(after)
	row.BeforeN, row.AfterN = len(before), len(after)
	if len(before) < minClearRuns || len(after) < minClearRuns {
		row.State = expectUnproven
		row.Description = fmt.Sprintf("unproven: n too small (before %d, after %d runs; %d needed on each side)", len(before), len(after), minClearRuns)
		return row
	}
	pct := map[string]int{"p50": 50, "p90": 90}[rec.e.Stat]
	row.Before, row.AfterValue = percentile(before, pct), percentile(after, pct)
	row.Clear, row.P = clearOf(after, before)
	return judgeExpectation(row, rec.e, secsText)
}

// readRate reads the wrong-block rate (waived denies over denies) on each side
// of the release. A rate has no per-run samples to test, so it holds when it
// moved the way the PR said, with denies enough on both sides to mean it.
func readRate(row ExpectationRow, e expect.Expectation, evs []stamped, newer func(string) (bool, bool)) ExpectationRow {
	sides := [2]*fold{newFold(), newFold()}
	for _, ev := range evs {
		if isAfter, known := newer(ev.BinVer); known {
			side := 0
			if isAfter {
				side = 1
			}
			sides[side].see(ev, true)
		}
	}
	var denies, waived [2]int
	for i, f := range sides {
		_, wrong, _, _ := f.results()
		for _, w := range wrong {
			denies[i] += w.Denies
			waived[i] += w.Waived
		}
	}
	row.Rate, row.BeforeN, row.AfterN = true, denies[0], denies[1]
	if denies[0] < minClearRuns || denies[1] < minClearRuns {
		row.State = expectUnproven
		row.Description = fmt.Sprintf("unproven: n too small (before %d, after %d denies; %d needed on each side)", denies[0], denies[1], minClearRuns)
		return row
	}
	row.Before = float64(waived[0]) / float64(denies[0])
	row.AfterValue = float64(waived[1]) / float64(denies[1])
	row.Clear = row.Before != row.AfterValue
	return judgeExpectation(row, e, percentText)
}

// pText is a p value to three places, or <0.001 when it rounds to none.
func pText(p float64) string {
	if p < 0.0005 {
		return "<0.001"
	}
	return fmt.Sprintf("%.3f", p)
}

func percentText(v float64) string { return strconv.FormatFloat(v*100, 'f', 1, 64) + "%" }

// judgeExpectation states whether the value moved the way the PR said, clearly.
func judgeExpectation(row ExpectationRow, e expect.Expectation, show func(float64) string) ExpectationRow {
	moved := fmt.Sprintf("%s -> %s", show(row.Before), show(row.AfterValue))
	counts := fmt.Sprintf("n %d -> %d", row.BeforeN, row.AfterN)
	if !row.Rate && row.P > 0 {
		counts += ", p " + pText(row.P)
	}
	right := (e.Direction == "down" && row.AfterValue < row.Before) || (e.Direction == "up" && row.AfterValue > row.Before)
	switch {
	case !row.Clear:
		row.State = expectNotHeld
		row.Description = fmt.Sprintf("not held: %s ~ no clear change (%s)", moved, counts)
	case right:
		row.State = expectHeld
		row.Description = fmt.Sprintf("held: %s (%s)", moved, counts)
	default:
		row.State = expectNotHeld
		row.Description = fmt.Sprintf("not held: %s, the other way (%s)", moved, counts)
	}
	return row
}

func (x Expectations) text(p func(string, ...any)) {
	if len(x.Rows) == 0 {
		return
	}
	p("")
	p("Expectations (a merged PR's expect: lines, read once %d lanes ran on a binary newer than its release; ~ is no clear change)", expectMinLanes)
	for _, r := range x.Rows {
		p("  PR #%s %s: %s", strings.TrimPrefix(r.PR, "#"), r.Expect, r.Description)
	}
}

// proposals are the expectations that did not hold, each a line to act on.
func (x Expectations) proposals() []Proposal {
	var out []Proposal
	for _, r := range x.Rows {
		if r.State == expectNotHeld {
			out = append(out, Proposal{Rule: "expect: PR #" + r.PR + " " + r.Expect,
				Numbers: r.Description,
				Change:  "the change did not move its metric as the PR said: find why, or revert it"})
		}
	}
	return out
}
