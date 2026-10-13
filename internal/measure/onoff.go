package measure

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// The on/off comparison: the lanes that ran with aphrollo on (the enforce and warn arms,
// which the red-green A/B goes on comparing inside) against the lanes that ran with it
// off. It is judged by the machinery of the A/B: a metrics table, 90% bootstrap
// intervals, a least evidence, the stop rule and its verdict words.

// onOffArms are the two sides, on first: every difference is on minus off, and a lower
// value is better for every metric.
var onOffArms = [2]string{"on", "off"}

// The verdict words of the comparison.
const (
	VerdictOnBetter  = "decided: on better"
	VerdictOffBetter = "decided: off better"
)

const offByDefinition = "off lanes count 0 by definition: no gate runs there"

// escapesOf is the escapes a lane let through: the log's records, a CI red after a local
// green, and the ones the tracker and main's CI blamed on it.
func escapesOf(f *abLaneFacts) float64 { return float64(f.records + f.ciRed + f.attributed) }

// onOffMetrics is the table, fixed before the data: the primary metric first.
var onOffMetrics = []abMetricDef{
	{Name: "escapes per lane", Role: "primary", Band: 0.05, Need: abEvidence{Lanes: 10, Events: 5}, Format: perLane, Stat: meanOf,
		Sample: func(f *abLaneFacts) (float64, bool) { return escapesOf(f), true }},
	{Name: "lead time p50", Role: "guardrail", Band: 1800, Need: abEvidence{Lanes: 10, N: 10}, Format: secs, Stat: medianOf,
		Sample: func(f *abLaneFacts) (float64, bool) {
			if f.mergeAt.IsZero() || f.firstSeen.IsZero() {
				return 0, false
			}
			return f.mergeAt.Sub(f.firstSeen).Seconds(), true
		}},
	{Name: "agent cost per lane (USD)", Role: "guardrail", Band: 0.5, Need: abEvidence{Lanes: 10, N: 10}, Format: perLane, Stat: meanOf,
		Sample: func(f *abLaneFacts) (float64, bool) { return f.cost, f.hasCost }},
	{Name: "friction: blocks per lane", Role: "guardrail", Band: 0.5, Need: abEvidence{Lanes: 10, Events: 5}, Format: perLane, Stat: meanOf, Note: offByDefinition,
		Sample: func(f *abLaneFacts) (float64, bool) {
			if f.arm == tddarm.ArmOff {
				return 0, true
			}
			return float64(f.denies + f.overrides + f.gateBlocks), true
		}},
	{Name: "friction: gate wait seconds per lane", Role: "guardrail", Band: 30, Need: abEvidence{Lanes: 10, Events: 5}, Format: secs, Stat: meanOf, Note: offByDefinition,
		Sample: func(f *abLaneFacts) (float64, bool) {
			if f.arm == tddarm.ArmOff {
				return 0, true
			}
			return f.gateSecs, true
		}},
}

// OnOffArm is one side: its lanes and the escapes they let through.
type OnOffArm struct {
	Arm     string `json:"arm"`
	Lanes   int    `json:"lanes"`
	Escapes int    `json:"escapes"`
}

// OnOffMetric is one metric's readout, on minus off.
type OnOffMetric struct {
	Name        string  `json:"name"`
	Role        string  `json:"role"`
	Band        float64 `json:"band"`
	On          float64 `json:"on"`
	Off         float64 `json:"off"`
	NOn         int     `json:"n_on"`
	NOff        int     `json:"n_off"`
	Diff        float64 `json:"diff"`
	Lo          float64 `json:"lo"`
	Hi          float64 `json:"hi"`
	HasInterval bool    `json:"has_interval"`
	Verdict     string  `json:"verdict"`
	Why         string  `json:"why,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// OnOff is the comparison's readout. Pinned lanes are on neither side.
type OnOff struct {
	Active    bool          `json:"active"`
	On        OnOffArm      `json:"on"`
	Off       OnOffArm      `json:"off"`
	Metrics   []OnOffMetric `json:"metrics,omitempty"`
	Verdict   string        `json:"verdict,omitempty"`
	Decidable bool          `json:"decidable"`
	Advice    string        `json:"advice,omitempty"`
	// Notes are what could not be read to make the comparison (the tracker, main's runs).
	Notes []string `json:"notes,omitempty"`
}

// summariseOnOff folds the lanes into the comparison. A lane is on in the enforce or warn
// arm, off in the off arm; one in no arm is on neither side.
func summariseOnOff(lanes map[string]*abLaneFacts) OnOff {
	side := map[string][]*abLaneFacts{}
	out := OnOff{On: OnOffArm{Arm: "on"}, Off: OnOffArm{Arm: "off"}}
	for _, f := range lanes {
		if f.pinned {
			continue
		}
		switch f.arm {
		case tddarm.ArmEnforce, tddarm.ArmWarn:
			out.On.Lanes++
			out.On.Escapes += int(escapesOf(f))
			side["on"] = append(side["on"], f)
		case tddarm.ArmOff:
			out.Off.Lanes++
			out.Off.Escapes += int(escapesOf(f))
			side["off"] = append(side["off"], f)
		}
	}
	if out.Off.Lanes == 0 {
		return OnOff{}
	}
	// Ordered, so the bootstrap draws the same lanes whatever order the map gave.
	for _, fs := range side {
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].firstSeen.Before(fs[j].firstSeen) })
	}
	out.Active = true
	for _, m := range foldMetricsFor(onOffMetrics, onOffArms, side) {
		out.Metrics = append(out.Metrics, OnOffMetric{Name: m.Name, Role: m.Role, Band: m.Band, On: m.Enforce, Off: m.Warn,
			NOn: m.NEnforce, NOff: m.NWarn, Diff: m.Diff, Lo: m.Lo, Hi: m.Hi, HasInterval: m.HasInterval,
			Verdict: onOffWord(m.Verdict), Why: m.Why, Note: m.Note})
	}
	out.Verdict = out.Metrics[0].Verdict
	out.Decidable = out.Verdict != VerdictDeciding
	if out.Verdict == VerdictMaxReached {
		out.Advice = TooSmall
		if out.Metrics[0].Why != "" {
			out.Advice = TooFewEvents
		}
	}
	return out
}

// onOffWord names the side a shared verdict word was about: the first arm of the A/B words
// is the on side, the second the off.
func onOffWord(v string) string {
	switch v {
	case VerdictEnforceBetter:
		return VerdictOnBetter
	case VerdictWarnBetter:
		return VerdictOffBetter
	}
	return v
}

// Text is the section as `stats --ab` prints it.
func (o OnOff) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p("on vs off (aphrollo on = the enforce and warn arms; a decision is forced at %d lanes a side):", MaxABLanes)
	p("  %-4s %d lanes  escapes %d", "on", o.On.Lanes, o.On.Escapes)
	p("  %-4s %d lanes  escapes %d", "off", o.Off.Lanes, o.Off.Escapes)
	for _, m := range o.Metrics {
		p("  %s", m.text())
	}
	p("verdict (%s): %s", onOffMetrics[0].Name, o.Verdict)
	if o.Advice != "" {
		p("  %s", o.Advice)
	}
	for _, n := range o.Notes {
		p("  note: %s", n)
	}
	return b.String()
}

func (m OnOffMetric) text() string {
	format := perLane
	for _, def := range onOffMetrics {
		if def.Name == m.Name {
			format = def.Format
		}
	}
	verdict := m.Verdict
	if m.Why != "" {
		verdict += " (" + m.Why + ")"
	}
	head := fmt.Sprintf("%s (%s): on %s (n %d), off %s (n %d)", m.Name, m.Role, format(m.On), m.NOn, format(m.Off), m.NOff)
	if m.Note != "" {
		head += " [" + m.Note + "]"
	}
	if !m.HasInterval {
		return head + "; no interval under 2 per side; " + verdict
	}
	return fmt.Sprintf("%s; on minus off %s, 90%% interval [%s, %s]; %s", head,
		signed(format, m.Diff), signed(format, m.Lo), signed(format, m.Hi), verdict)
}
