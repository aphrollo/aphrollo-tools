package cli

import (
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// redGreenOverride is what a deny of the red→green rule offers: the verb that waives
// it for the session, and the key that leaves the experiment for good. The rule
// table's own text names the trellis verbs, and this hook has its own.
const redGreenOverride = "aphrollo gate allow red-green (this session), or aphrollo config set tdd warn"

// redGreenLive is the red→green rule live at PreToolUse (docs/trellis-architecture.md §4
// and §5, lane A1). For a call that is about to write a code file of a lane, it asks
// the kernel, under the mode the lane runs in (tddarm: a pin, else the lane's arm), what
// to say: guidance of at most 60 tokens under warn, a deny of at most 120 tokens that
// names its override under enforce, nothing under off. The answer is the rule table's:
// this only reads the lane's record and renders the line.
//
// It changes nothing for a call another judgement already blocked, and nothing at all
// under tdd = off. A question that does not finish inside shadow.LiveBudget says
// nothing; the record of the call then says it was dropped for the budget. It spawns
// no git: the mode, the lane and the record are read from files.
func (p *preShadow) redGreenLive(final tdd.Decision) tdd.Decision {
	if final.Action == tdd.Block {
		return final
	}
	pl, ok := shadow.ParsePayload(p.raw)
	if !ok {
		return final
	}
	files := pl.Targets()
	if !writesCode(files) {
		return final
	}
	src, ok := hookSource(p.raw)
	if !ok {
		return final
	}
	m := tdd.EffectiveTDD(src.Root)
	if m.TDD == string(kernel.ModeOff) || m.Why == tddarm.WhyNoLane {
		return final
	}
	p.mode = m
	tdd.RecordLaneArm(src.Root, src.Actor, m)
	res := tdd.ShadowWorld().RedGreenLive(pl, files, kernel.Config{TDD: kernel.TDDMode(m.TDD)})
	p.live, p.liveAsked = res, true
	if res.Overran {
		return final
	}
	var fired []shadow.Live
	for _, l := range res.Asked {
		if l.Fires() {
			fired = append(fired, l)
		}
	}
	if len(fired) == 0 {
		return final
	}
	first := fired[0]
	deny := first.Decision.Outcome == kernel.OutcomeDeny
	if deny && tdd.RedGreenWaived(pl.SessionID) {
		return final // the session waived it; the override was counted when it was made
	}
	line := render.Untested(first.Decision, unitList(fired), redGreenOverride)
	if !deny {
		d := tdd.Decision{Action: tdd.Warn, Reason: line.Text, Policy: "red-green"}
		if final.Action == tdd.Warn && final.Reason != "" {
			d.Reason = strings.TrimRight(final.Reason, "\n") + "\n" + line.Text
			d.Policy = final.Policy
		}
		return d
	}
	d := tdd.Decision{Action: tdd.Block, Reason: line.Text, Policy: "red-green", Override: redGreenOverride}
	tdd.LogEditDecision(p.raw, d)
	p.liveBlocked = true
	return d
}

// unitList names the units a call asked about: the first, and how many more.
func unitList(fired []shadow.Live) string {
	if len(fired) == 1 {
		return fired[0].Unit
	}
	return fired[0].Unit + " +" + strconv.Itoa(len(fired)-1) + " more"
}

// writesCode reports whether any of the files a call writes is a code file.
func writesCode(files []string) bool {
	for _, f := range files {
		if shadow.FileClassOf(f) == kernel.ClassCode {
			return true
		}
	}
	return false
}
