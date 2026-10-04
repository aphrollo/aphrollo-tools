package measure

import (
	"fmt"
	"slices"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ShadowNoFires is what a deny says when no shadow fire names its rule as the
// live rule: the kernel decides nothing a hook records beside it.
const ShadowNoFires = "no shadow fires recorded for this rule"

// What followed a shadowed would-be block, as `why` says it.
const (
	ShadowWrong     = "wrong block: an override followed within 10 min"
	ShadowCatch     = "catch: a commit gate refusal, red CI or escape followed on the lane"
	ShadowPass      = "pass: the lane merged with none of those"
	ShadowOpen      = "open: nothing downstream yet"
	ShadowNotABlock = "not a would-be block: trellis did not block where aphrollo allowed"
)

// ShadowWhy replays a shadow event: what the two sides decided, how they
// compare, what followed it on its lane when trellis would have blocked where
// aphrollo did not, and the rule's counts over the whole log.
type ShadowWhy struct {
	Rule     string     `json:"rule"`
	LiveRule string     `json:"live_rule,omitempty"`
	Trellis  string     `json:"trellis"`
	Aphrollo string     `json:"aphrollo"`
	Relation string     `json:"relation"`
	HeldOut  bool       `json:"held_out"`
	Key      string     `json:"key,omitempty"`
	Outcome  string     `json:"outcome"`
	Counts   ShadowRule `json:"rule_counts"`
}

// summary is the rule's counts as one line.
func (r ShadowRule) summary() string {
	return fmt.Sprintf("%d fires: agree %d, would-be block %d, softer %d, mismatch %d, held out %d",
		r.Fires, r.Agree, r.Stricter, r.Softer, r.Mismatch, r.HeldOut)
}

func explainShadow(events []tdd.Event, e tdd.Event) *ShadowWhy {
	d := e.Detail
	s := &ShadowWhy{Rule: d["rule"], LiveRule: d["live_rule"], Trellis: d["trellis"], Aphrollo: d["aphrollo"],
		Relation: d["relation"], HeldOut: d["held_out"] == "true", Key: d["key"], Outcome: ShadowNotABlock}
	for _, r := range ComputeShadow(events, time.Time{}, Options{}).Rules {
		if r.Rule == s.Rule {
			s.Counts = r
		}
	}
	if s.Relation != "trellis-stricter" {
		return s
	}
	evs := newScope(events, time.Time{}, Options{}).evs
	i := slices.IndexFunc(evs, func(x stamped) bool { return x.Seq == e.Seq })
	switch {
	case i < 0:
		s.Outcome = ShadowOpen
	default:
		s.Outcome = map[follow]string{followWrong: ShadowWrong, followCatch: ShadowCatch, followPass: ShadowPass, followOpen: ShadowOpen}[followOf(evs, i)]
	}
	return s
}

// shadowOfDeny is what a deny says of the shadow fires recorded under its live
// rule: the counts of every shadow event whose live_rule is the deny's rule.
func shadowOfDeny(events []tdd.Event, rule string) string {
	var r ShadowRule
	for _, e := range events {
		if e.Kind != "shadow" || e.Detail["live_rule"] != rule {
			continue
		}
		r.Fires++
		switch e.Detail["relation"] {
		case "agree":
			r.Agree++
		case "trellis-stricter":
			r.Stricter++
		case "trellis-softer":
			r.Softer++
		case "verdict-mismatch":
			r.Mismatch++
		}
		if e.Detail["held_out"] == "true" {
			r.HeldOut++
		}
	}
	if r.Fires == 0 {
		return ShadowNoFires
	}
	return r.summary()
}
