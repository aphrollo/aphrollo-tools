package measure

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// What happened after a deny, read from the events of its lane.
const (
	OutcomeOverridden     = "overridden"                      // an override within WrongBlockWindow: a wrong block
	OutcomeOverriddenLate = "overridden late"                 // the next thing the agent did was an override, after the window
	OutcomeRepeated       = "repeated"                        // the same rule denied again: not compliance
	OutcomeComplied       = "complied"                        // no override, and the agent went on to other work
	OutcomeNothingAfter   = "nothing recorded after the deny" // the lane has no later edit, run, deny or override
)

// ShadowNotRecorded is what a deny says of shadow catches and passes until the
// log carries shadow events.
const ShadowNotRecorded = "not recorded yet"

// Why is the answer to `why <seq>`: the event as the log recorded it, and for a
// deny or a run result what the rest of the log says of it. Deny and Run are nil
// for any other kind.
type Why struct {
	Seq     int64             `json:"seq"`
	At      string            `json:"at"`
	Lane    string            `json:"lane,omitempty"`
	Actor   string            `json:"actor,omitempty"`
	Kind    string            `json:"kind"`
	Stage   string            `json:"stage,omitempty"`
	Verdict string            `json:"verdict,omitempty"`
	Detail  map[string]string `json:"detail,omitempty"`
	Deny    *DenyWhy          `json:"deny,omitempty"`
	Run     *RunWhy           `json:"run,omitempty"`
	Shadow  string            `json:"shadow,omitempty"`
}

// DenyWhy replays a deny. Override is the first override on the lane after it, at any age; WrongBlock
// is whether one came within WrongBlockWindow. Counts are over the whole log.
type DenyWhy struct {
	Rule            string        `json:"rule"`
	Cause           string        `json:"cause,omitempty"`
	OfferedOverride string        `json:"offered_override,omitempty"`
	Override        *OverrideSeen `json:"override,omitempty"`
	WrongBlock      bool          `json:"wrong_block"`
	Outcome         string        `json:"outcome"`
	Counts          RuleCounts    `json:"rule_counts"`
	Kernel          *KernelRule   `json:"kernel,omitempty"`
}

// OverrideSeen is an override that followed a deny.
type OverrideSeen struct {
	Seq       int64   `json:"seq"`
	Name      string  `json:"name"`
	AfterSecs float64 `json:"after_secs"`
}

// RuleCounts are one rule's tallies over the log. Overrides and WrongBlocks
// count the override events that follow a deny of the rule on its lane, so they
// add up to the stats fold; Complied counts denies.
type RuleCounts struct {
	Denies      int `json:"denies"`
	Overrides   int `json:"overrides"`
	WrongBlocks int `json:"wrong_blocks"`
	Complied    int `json:"complied"`
}

// KernelRule is the rule's row in the kernel's table, when it has one. The
// level is the default: a repo's pin is not read here.
type KernelRule struct {
	Level      string `json:"level"`
	Section    string `json:"section"`
	Class      string `json:"class"`
	Shadowable bool   `json:"shadowable"`
	InHoldout  bool   `json:"in_holdout"`
}

// RunWhy replays a run result. Tree and Cause are "" when the event did not
// record them (the v1 writer records no tree); LatencyMs is nil likewise.
type RunWhy struct {
	Verdict   string   `json:"verdict"`
	Result    string   `json:"result,omitempty"`
	Cause     string   `json:"cause,omitempty"`
	NotTested bool     `json:"not_tested"`
	Tree      string   `json:"tree,omitempty"`
	Edit      string   `json:"edit,omitempty"`
	LatencyMs *float64 `json:"latency_ms,omitempty"`
}

// Explain finds the event numbered seq and folds the log around it. The answer
// depends only on the events, never on their order in the slice.
func Explain(events []tdd.Event, seq int64) (Why, bool) {
	i := slices.IndexFunc(events, func(e tdd.Event) bool { return e.Seq == seq })
	if i < 0 {
		return Why{}, false
	}
	e := events[i]
	w := Why{Seq: e.Seq, At: e.At, Lane: e.Lane, Actor: e.Actor, Kind: e.Kind, Stage: e.Stage, Verdict: e.Verdict, Detail: e.Detail}
	switch e.Kind {
	case "deny":
		w.Deny, w.Shadow = explainDeny(events, e), ShadowNotRecorded
	case "run.result":
		w.Run = explainRun(e)
	}
	return w, true
}

func explainRun(e tdd.Event) *RunWhy {
	r := &RunWhy{Verdict: e.Verdict, Result: e.Detail["result"], Cause: e.Detail["cause"],
		NotTested: e.Detail["result"] == "not-tested", Tree: e.Detail["tree"], Edit: e.Detail["edit"]}
	if ms, err := strconv.ParseFloat(e.Detail["latency_ms"], 64); err == nil {
		r.LatencyMs = &ms
	}
	return r
}

// denyFacts is one deny with what followed it on its lane: the overrides that
// name it as the lane's latest deny, and the next event the agent caused.
type denyFacts struct {
	ev        stamped
	overrides []stamped
	next      *stamped
}

// agentMoved is the kinds that show what the agent did next; timing and CI
// events are the box's, not the agent's.
func agentMoved(kind string) bool {
	return kind == "deny" || kind == "override" || kind == "edit" || kind == "run.result"
}

// followDenies reads the time-ordered events once. An override belongs to the
// latest deny on its lane, as the stats fold attributes it.
func followDenies(evs []stamped) []*denyFacts {
	var all []*denyFacts
	latest := map[string]*denyFacts{}
	waiting := map[string][]*denyFacts{}
	for i := range evs {
		e := evs[i]
		if !agentMoved(e.Kind) {
			continue
		}
		for _, d := range waiting[e.Lane] {
			d.next = &evs[i]
		}
		delete(waiting, e.Lane)
		switch e.Kind {
		case "deny":
			d := &denyFacts{ev: e}
			all = append(all, d)
			latest[e.Lane] = d
			waiting[e.Lane] = append(waiting[e.Lane], d)
		case "override":
			if d := latest[e.Lane]; d != nil {
				d.overrides = append(d.overrides, e)
			}
		}
	}
	return all
}

func (d *denyFacts) wrongBlocks() (n int) {
	for _, o := range d.overrides {
		if o.at.Sub(d.ev.at) <= WrongBlockWindow {
			n++
		}
	}
	return n
}

func (d *denyFacts) outcome() string {
	switch {
	case d.wrongBlocks() > 0:
		return OutcomeOverridden
	case d.next == nil:
		return OutcomeNothingAfter
	case d.next.Kind == "override":
		return OutcomeOverriddenLate
	case d.next.Kind == "deny" && denyRule(*d.next) == denyRule(d.ev):
		return OutcomeRepeated
	}
	return OutcomeComplied
}

func explainDeny(events []tdd.Event, e tdd.Event) *DenyWhy {
	d := &DenyWhy{Rule: denyRule(stamped{Event: e}), Cause: e.Detail["cause"], OfferedOverride: e.Detail["override"],
		Outcome: "unknown: the event's time is not valid"}
	var evs []stamped
	for _, x := range events {
		if at, err := time.Parse(time.RFC3339, x.At); err == nil {
			evs = append(evs, stamped{x, at})
		}
	}
	sort.SliceStable(evs, func(i, j int) bool {
		if !evs[i].at.Equal(evs[j].at) {
			return evs[i].at.Before(evs[j].at)
		}
		return evs[i].Seq < evs[j].Seq
	})
	for _, f := range followDenies(evs) {
		if denyRule(f.ev) != d.Rule {
			continue
		}
		d.Counts.Denies++
		d.Counts.Overrides += len(f.overrides)
		d.Counts.WrongBlocks += f.wrongBlocks()
		if f.outcome() == OutcomeComplied {
			d.Counts.Complied++
		}
		if f.ev.Seq == e.Seq {
			d.fill(f)
		}
	}
	if r, ok := kernel.LookupRule(d.Rule); ok {
		d.Kernel = &KernelRule{Level: string(r.DefaultLevel()), Section: r.Section, Class: string(r.Class),
			Shadowable: r.Class == kernel.RuleEarned, InHoldout: kernel.InHoldout(e.Lane, r)}
	}
	return d
}

func (d *DenyWhy) fill(f *denyFacts) {
	d.Outcome, d.WrongBlock = f.outcome(), f.wrongBlocks() > 0
	if len(f.overrides) > 0 {
		o := f.overrides[0]
		d.Override = &OverrideSeen{Seq: o.Seq, Name: o.Detail["override"], AfterSecs: o.at.Sub(f.ev.at).Seconds()}
	}
}
