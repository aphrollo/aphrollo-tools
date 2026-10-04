package measure

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// MinShadowFires is how many shadow fires a rule needs before a rate is printed
// for it (docs/trellis-architecture.md §5: a rule keeps or changes its level only
// over its last 10 or more fires). Under it the report says so.
const MinShadowFires = 10

// ShadowHorizonDays is how long after a would-be block a downstream authority
// may still make it a catch, and a merge a pass: the document's 28 days of a
// merged change staying clean, as far as the log reaches.
const ShadowHorizonDays = 28

const shadowHorizon = ShadowHorizonDays * 24 * time.Hour

// ShadowRule is one rule's shadow fires: what the kernel would have decided
// beside what aphrollo did (internal/shadow). A disagreement is not a wrong
// block. Of the fires where trellis would block and aphrollo did not (Stricter,
// the would-be blocks), the log afterwards says which were wrong (an override on
// the lane within WrongBlockWindow), which were catches (a later commit gate
// refusal, red CI or escape on the lane), which passed (the lane merged with none
// of those) and which are still open. Softer is where aphrollo denies and trellis
// would only guide; SoftHeldOut those that were the holdout arm's by design.
type ShadowRule struct {
	Rule        string `json:"rule"`
	Fires       int    `json:"fires"`
	Agree       int    `json:"agree"`
	Stricter    int    `json:"trellis_stricter"`
	Softer      int    `json:"trellis_softer"`
	SoftHeldOut int    `json:"trellis_softer_held_out"`
	Mismatch    int    `json:"verdict_mismatch"`
	HeldOut     int    `json:"held_out"`
	Catches     int    `json:"would_be_catches"`
	Wrong       int    `json:"would_be_wrong"`
	Passes      int    `json:"would_be_passes"`
	Open        int    `json:"would_be_open"`
}

// Rate is the share of the rule's fires where both sides agreed, "" while the
// rule has fewer than MinShadowFires.
func (r ShadowRule) Rate() string {
	if r.Fires < MinShadowFires {
		return ""
	}
	return fmt.Sprintf("%.0f%%", share(r.Agree, r.Fires)*100)
}

// Shadow is the shadow fires of a window, by rule.
type Shadow struct {
	Window string       `json:"window"`
	Fires  int          `json:"fires"`
	Rules  []ShadowRule `json:"rules"`
}

// ComputeShadow folds the shadow events of the window. What follows a fire is
// read from the whole log, not only the window.
func ComputeShadow(events []tdd.Event, now time.Time, o Options) Shadow {
	s := newScope(events, now, o)
	out := Shadow{Window: "whole log"}
	if o.Window > 0 {
		out.Window = "last " + windowText(o.Window)
	}
	rules := map[string]*ShadowRule{}
	for i, e := range s.evs {
		if e.Kind != "shadow" || !s.in(e.at) {
			continue
		}
		rule := e.Detail["rule"]
		if rule == "" {
			rule = "unknown"
		}
		r := rules[rule]
		if r == nil {
			r = &ShadowRule{Rule: rule}
			rules[rule] = r
		}
		out.Fires++
		r.Fires++
		held := e.Detail["held_out"] == "true"
		if held {
			r.HeldOut++
		}
		switch e.Detail["relation"] {
		case "agree":
			r.Agree++
		case "trellis-stricter":
			r.Stricter++
			switch followOf(s.evs, i) {
			case followWrong:
				r.Wrong++
			case followCatch:
				r.Catches++
			case followPass:
				r.Passes++
			default:
				r.Open++
			}
		case "trellis-softer":
			r.Softer++
			if held {
				r.SoftHeldOut++
			}
		case "verdict-mismatch":
			r.Mismatch++
		}
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, *r)
	}
	sort.Slice(out.Rules, func(i, j int) bool { return out.Rules[i].Rule < out.Rules[j].Rule })
	return out
}

type follow int

const (
	followOpen follow = iota
	followWrong
	followCatch
	followPass
)

// followOf is what the lane's log says of the would-be block at evs[i]: an
// override within WrongBlockWindow makes it wrong, whatever else follows; else a
// later commit gate refusal, red CI or escape within the horizon makes it a catch;
// else a merge makes it a pass. A fire on no lane has nothing to follow.
func followOf(evs []stamped, i int) follow {
	fire := evs[i]
	if fire.Lane == "" {
		return followOpen
	}
	var caught, merged bool
	for _, e := range evs[i+1:] {
		if e.at.Sub(fire.at) > shadowHorizon {
			break
		}
		if e.Lane != fire.Lane {
			continue
		}
		switch {
		case e.Kind == "override" && !isAllowedRerun(e) && e.at.Sub(fire.at) <= WrongBlockWindow:
			return followWrong
		case e.Kind == "commit_gate_result" && e.Verdict == "blocked",
			e.Kind == "ci" && e.Verdict == "red",
			e.Kind == "escape" && e.Verdict != "false-positive":
			caught = true
		case e.Kind == "merge" && e.Verdict == "ok":
			merged = true
		}
	}
	switch {
	case caught:
		return followCatch
	case merged:
		return followPass
	}
	return followOpen
}

func windowText(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// Text is the shadow section as the plain lines `aphrollo stats --shadow` prints.
func (s Shadow) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	const cont = "                       " // under a row's first count
	p("%-22s%d (%s)", "shadow fires", s.Fires, s.Window)
	p("%-22s%s", "  wrong block", fmt.Sprintf("an override within %.0f min of the fire on its lane", WrongBlockWindow.Minutes()))
	p("%-22s%s", "  catch / pass", fmt.Sprintf("a later commit gate refusal, red CI or escape / a merge, within %d days", ShadowHorizonDays))
	for _, r := range s.Rules {
		p("%-22s %d fires  agree %d  would-be block %d  softer %d (%d held out)  mismatch %d  held out %d",
			r.Rule, r.Fires, r.Agree, r.Stricter, r.Softer, r.SoftHeldOut, r.Mismatch, r.HeldOut)
		if r.Stricter > 0 {
			p("%swould-be blocks: catches %d  wrong %d  passes %d  open %d", cont, r.Catches, r.Wrong, r.Passes, r.Open)
		}
		if rate := r.Rate(); rate != "" {
			p("%sagreement %s of %d fires", cont, rate, r.Fires)
		} else {
			p("%sunder %d fires: no rate", cont, MinShadowFires)
		}
	}
	return b.String()
}
