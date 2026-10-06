package report

import (
	"fmt"
	"sort"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// The thresholds a proposal needs before it is made. Each is evidence enough to
// look, never to act: the report only proposes, and a person or a lane applies
// the change. measure has no decision step of its own, so the table lives here.
const (
	// proposeMinDenies and proposeWaivedShare: a rule denied this often with at
	// least this share waived by an override looks like a wrong block.
	proposeMinDenies   = 5
	proposeWaivedShare = 0.5
	// proposeShadowWrongShare: of the kernel's would-be blocks, this share wrong
	// (over measure.MinShadowFires fires) says its stricter call is not yet right.
	proposeShadowWrongShare = 0.2
	// proposeSecsLost is the seconds one rule's refusals and untested stages may
	// cost in the window before it is named a time sink.
	proposeSecsLost = 600
	// proposeNotTested is the untested runs one cause may leave in the window.
	proposeNotTested = 10
	// proposeStanddowns is how often a matcher kind may stand a rule down.
	proposeStanddowns = 20
)

// propose is the proposals the report's own numbers support, in a stable order.
func propose(r Report) []Proposal {
	var out []Proposal
	for _, w := range r.WrongBlocks {
		if w.Denies >= proposeMinDenies && float64(w.Waived) >= proposeWaivedShare*float64(w.Denies) {
			out = append(out, Proposal{Rule: w.Rule,
				Numbers: fmt.Sprintf("%d denies, %d waived by an override within %s (%s)", w.Denies, w.Waived, measure.WrongBlockWindow, w.Rate),
				Change:  "lower the rule from block to guide, or narrow its matcher so the waived cases pass",
				Refs:    w.Refs})
		}
	}
	for _, s := range r.ShadowWrong {
		if s.Fires >= measure.MinShadowFires && float64(s.Wrong) >= proposeShadowWrongShare*float64(s.Stricter) && s.Wrong > 0 {
			out = append(out, Proposal{Rule: s.Rule,
				Numbers: fmt.Sprintf("%d fires, %d would-be blocks, %d wrong", s.Fires, s.Stricter, s.Wrong),
				Change:  "keep the kernel at guide for this rule until its would-be blocks stop being wrong"})
		}
	}
	for _, f := range r.Friction {
		if f.SecsLost >= proposeSecsLost {
			out = append(out, Proposal{Rule: f.Rule,
				Numbers: fmt.Sprintf("%.0fs lost over %d refusals and %d untested stages", f.SecsLost, f.Refusals, f.NotTested),
				Change:  "make the stage cheaper or move the check earlier, to the edit",
				Refs:    f.Refs})
		}
		if f.NotTested >= proposeNotTested {
			out = append(out, Proposal{Rule: f.Rule,
				Numbers: fmt.Sprintf("%d runs that proved nothing", f.NotTested),
				Change:  "find why the work did not run: a run that proved nothing is not a green",
				Refs:    f.Refs})
		}
	}
	for _, s := range r.Standdowns {
		if s.N >= proposeStanddowns {
			out = append(out, Proposal{Rule: s.Matcher,
				Numbers: fmt.Sprintf("%d stand-downs", s.N),
				Change:  "teach the kernel the matcher, or drop the law row that names it",
				Refs:    s.Refs})
		}
	}
	for _, b := range r.Tokens.Briefs {
		if b.Over {
			out = append(out, Proposal{Rule: b.Name,
				Numbers: fmt.Sprintf("%d tokens against a cap of %d", b.Tokens, b.Cap),
				Change:  "trim the text to its cap"})
		}
	}
	if r.ABTotal.Decidable {
		out = append(out, Proposal{Rule: "red-green",
			Numbers: abLanes(r.ABTotal),
			Change:  "read the arms side by side, then decide enforce or warn: the owner's call"})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return out[i].Rule < out[j].Rule
		}
		return out[i].Change < out[j].Change
	})
	return out
}

// abLanes is the lanes each arm holds against the number it needs.
func abLanes(ab measure.AB) string {
	s := ""
	for i, a := range ab.Arms {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s %d of %d lanes", a.Arm, a.Lanes, measure.MinABLanes)
	}
	return s
}
