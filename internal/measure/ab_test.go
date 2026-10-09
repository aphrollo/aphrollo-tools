package measure

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func computeAB(events []tdd.Event, o Options) AB {
	return ComputeAB(events, base.Add(40*24*time.Hour), o)
}

// abLane is a lane in an arm: its lane-arm event, and what the hook then did about code
// edits there (acts: "warn", "block" or "allow" per decision, in the language lang).
func abLane(sec float64, lane, arm, lang string, acts ...string) []tdd.Event {
	out := []tdd.Event{ev(sec, lane, "lane-arm", detail("arm", arm, "why", "assigned", "mode", arm))}
	for i, a := range acts {
		out = append(out, shadowAt(sec+1+float64(i), lane, "red-green", "trellis-stricter",
			"aphrollo", a, "arm", arm, "arm_why", "assigned", "tdd", arm, "lang", lang, "unit", "u"))
	}
	return out
}

func abRow(t *testing.T, ab AB, arm string) ABArm {
	t.Helper()
	for _, a := range ab.Arms {
		if a.Arm == arm {
			return a
		}
	}
	t.Fatalf("no row for the %s arm in %+v", arm, ab.Arms)
	return ABArm{}
}

func abCat(parts ...[]tdd.Event) []tdd.Event {
	var out []tdd.Event
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Per arm: the lanes assigned to it, the red-green denies and warnings the hook gave
// there, and the overrides of the deny.
func TestComputeAB_CountsLanesDeniesWarningsAndOverridesPerArm(t *testing.T) {
	events := abCat(
		abLane(0, "lane/e1", "enforce", "go", "block", "block", "allow"),
		abLane(0, "lane/e2", "enforce", "python", "block"),
		abLane(0, "lane/w1", "warn", "go", "warn", "allow"),
		abLane(0, "lane/w2", "warn", "ts", "warn", "warn"),
		[]tdd.Event{
			ev(50, "lane/e1", "override", detail("override", "override-red-green-allow")),
			ev(51, "lane/e1", "override", detail("override", "override-primary-allow")),
			ev(52, "lane/w1", "override", detail("override", "override-red-green-allow")),
		},
	)
	ab := computeAB(events, Options{})
	e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn")
	if e.Lanes != 2 || e.Denies != 3 || e.Warnings != 0 || e.Overrides != 1 {
		t.Errorf("enforce = %+v, want 2 lanes, 3 denies, 0 warnings, 1 red-green override", e)
	}
	if w.Lanes != 2 || w.Denies != 0 || w.Warnings != 3 || w.Overrides != 1 {
		t.Errorf("warn = %+v, want 2 lanes, 0 denies, 3 warnings, 1 override", w)
	}
}

// A pinned lane is outside the experiment: counted apart, in no arm's row.
func TestComputeAB_APinnedLaneIsOutsideBothArms(t *testing.T) {
	events := abCat(
		abLane(0, "lane/w1", "warn", "go", "warn"),
		[]tdd.Event{
			ev(0, "lane/p1", "lane-arm", detail("why", "pinned", "mode", "enforce", "layer", "repo")),
			shadowAt(1, "lane/p1", "red-green", "trellis-stricter", "aphrollo", "block", "arm_why", "pinned", "tdd", "enforce", "lang", "go"),
		},
	)
	ab := computeAB(events, Options{})
	if ab.Pinned != 1 {
		t.Errorf("Pinned = %d, want 1", ab.Pinned)
	}
	if e := abRow(t, ab, "enforce"); e.Lanes != 0 || e.Denies != 0 {
		t.Errorf("enforce = %+v, want the pinned lane's deny left out", e)
	}
}

// Escapes of a lane count in its arm: the escape records, and a CI red after a local
// green, joined by lane. A CI red with no local green before it is no escape of the gate.
func TestComputeAB_EscapesAreTheLanesEscapeRecordsAndItsCIRedAfterALocalGreen(t *testing.T) {
	green := func(sec float64, lane string) tdd.Event {
		return ev(sec, lane, "stage.timing", func(e *tdd.Event) { e.Stage, e.Verdict = "postedit", "green (4 passed)" })
	}
	ciRed := func(sec float64, lane string) tdd.Event {
		return ev(sec, lane, "ci", func(e *tdd.Event) { e.Verdict = "red" })
	}
	events := abCat(
		abLane(0, "lane/w1", "warn", "go", "warn"),
		abLane(0, "lane/w2", "warn", "go", "warn"),
		abLane(0, "lane/e1", "enforce", "go", "block"),
		[]tdd.Event{
			green(10, "lane/w1"), ciRed(20, "lane/w1"), // an escape
			ciRed(21, "lane/w1"), // the same lane's second red: not another
			ciRed(30, "lane/w2"), // never green locally: not an escape of the gate
			ev(40, "lane/w2", "escape", func(e *tdd.Event) { e.Verdict = "escaped"; e.Detail = map[string]string{"class": "product"} }),
			ev(41, "lane/e1", "escape", func(e *tdd.Event) { e.Verdict = "false-positive" }), // a wrong deny, not an escape
		},
	)
	ab := computeAB(events, Options{})
	w, e := abRow(t, ab, "warn"), abRow(t, ab, "enforce")
	if w.Escapes != 2 || w.EscapeRecords != 1 || w.CIRedAfterGreen != 1 {
		t.Errorf("warn = %+v, want 2 escapes: 1 record and 1 CI red after a local green", w)
	}
	if e.Escapes != 0 {
		t.Errorf("enforce = %+v, want no escapes: a false positive is no escape", e)
	}
}

// Time to green: from the lane's first red-green decision to its next green.
func TestComputeAB_TimeToGreenRunsFromTheFirstDecisionToTheNextGreen(t *testing.T) {
	green := func(sec float64, lane string) tdd.Event {
		return ev(sec, lane, "stage.timing", func(e *tdd.Event) { e.Stage, e.Verdict = "postedit", "green (1 passed)" })
	}
	events := abCat(
		abLane(100, "lane/w1", "warn", "go", "warn"), []tdd.Event{green(160, "lane/w1")},
		abLane(100, "lane/w2", "warn", "go", "warn"), []tdd.Event{green(400, "lane/w2")},
		abLane(100, "lane/w3", "warn", "go", "warn"), // never green: no sample
	)
	w := abRow(t, computeAB(events, Options{}), "warn")
	// the decisions are one second after the lane's first sight, so 59 s and 299 s
	if w.TimeToGreen.N != 2 || w.TimeToGreen.P50 != 59 || w.TimeToGreen.P90 != 299 {
		t.Errorf("time to green = %+v, want n=2, p50 59 s, p90 299 s", w.TimeToGreen)
	}
}

// Per language, a lane is in each language it had a decision in.
func TestComputeAB_PerLanguageRowsCountTheLanesAndTheirDecisions(t *testing.T) {
	events := abCat(
		abLane(0, "lane/w1", "warn", "go", "warn", "warn"),
		abLane(0, "lane/w2", "warn", "python", "warn"),
		abLane(0, "lane/e1", "enforce", "ts", "block"),
	)
	ab := computeAB(events, Options{})
	got := map[string]ABLang{}
	for _, l := range ab.Languages {
		got[l.Arm+"/"+l.Lang] = l
	}
	if g := got["warn/go"]; g.Lanes != 1 || g.Warnings != 2 {
		t.Errorf("warn/go = %+v, want 1 lane, 2 warnings", g)
	}
	if g := got["warn/python"]; g.Lanes != 1 || g.Warnings != 1 {
		t.Errorf("warn/python = %+v", g)
	}
	if g := got["enforce/ts"]; g.Lanes != 1 || g.Denies != 1 {
		t.Errorf("enforce/ts = %+v, want 1 lane, 1 deny", g)
	}
}

// A decision whose record is the hook dropping it for its budget is counted apart, never
// as a deny or a warning.
func TestComputeAB_ADroppedDecisionIsCountedApart(t *testing.T) {
	events := abCat(abLane(0, "lane/w1", "warn", "go"), []tdd.Event{
		shadowAt(1, "lane/w1", "red-green", "unjudged", "arm", "warn", "arm_why", "assigned", "cause", "budget"),
	})
	if w := abRow(t, computeAB(events, Options{}), "warn"); w.Dropped != 1 || w.Denies != 0 || w.Warnings != 0 {
		t.Errorf("warn = %+v, want one dropped decision and no deny or warning", w)
	}
}

func TestAB_TextNamesEachArmItsCountsAndTheMaximumLanes(t *testing.T) {
	events := abCat(abLane(0, "lane/e1", "enforce", "go", "block"), abLane(0, "lane/w1", "warn", "go", "warn"))
	text := computeAB(events, Options{}).Text()
	for _, want := range []string{"enforce", "warn", "1 lanes (a decision is forced at 50 per arm)", "denies", "warnings", "overrides", "escapes", "time to green"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

// The kernel's holdout turns an enforce lane's deny into a guide, recorded as a warning
// with held_out: it is no warning of the warn arm's kind, and no friction of the enforce
// arm's. It is counted apart, in the arm, and shown.
func TestComputeAB_AHeldOutDecisionIsCountedApartFromTheArmsDeniesWarningsAndFriction(t *testing.T) {
	events := abCat(
		abLane(0, "lane/e1", "enforce", "go", "block"),
		[]tdd.Event{
			ev(0, "lane/e2", "lane-arm", detail("arm", "enforce", "why", "assigned", "mode", "enforce")),
			shadowAt(1, "lane/e2", "red-green", "trellis-stricter", "aphrollo", "warn", "held_out", "true",
				"arm", "enforce", "arm_why", "assigned", "tdd", "enforce", "lang", "go"),
			ev(60, "lane/e2", "stage.timing", func(e *tdd.Event) { e.Stage, e.Verdict = "postedit", "green (1 passed)" }),
		},
	)
	ab := computeAB(events, Options{})
	e := abRow(t, ab, "enforce")
	if e.Lanes != 2 || e.Denies != 1 || e.Warnings != 0 || e.HeldOut != 1 || e.TimeToGreen.N != 0 {
		t.Errorf("enforce = %+v, want 2 lanes, 1 deny, no warning, 1 held out and no time to green from the held-out decision", e)
	}
	if !strings.Contains(ab.Text(), "held out 1") {
		t.Errorf("text lacks the held-out count:\n%s", ab.Text())
	}
	for _, l := range ab.Languages {
		if l.Arm == "enforce" && l.Lang == "go" && (l.Warnings != 0 || l.Lanes != 1) {
			t.Errorf("enforce/go = %+v, want the held-out decision left out", l)
		}
	}
}

// The rows come in one order whatever the order of the log: arm, then language by name.
// Text() of the same log is the same bytes every run.
func TestComputeAB_LanguagesAreOrderedByArmThenNameAndTheTextIsStable(t *testing.T) {
	var events []tdd.Event
	for i, lang := range []string{"ts", "go", "python"} {
		events = append(events,
			abLane(0, fmt.Sprintf("lane/w%d", i), "warn", lang, "warn")...)
		events = append(events,
			abLane(0, fmt.Sprintf("lane/e%d", i), "enforce", lang, "block")...)
	}
	want := []string{"enforce/go", "enforce/python", "enforce/ts", "warn/go", "warn/python", "warn/ts"}
	first := computeAB(events, Options{})
	var got []string
	for _, l := range first.Languages {
		got = append(got, l.Arm+"/"+l.Lang)
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("languages = %v, want %v", got, want)
	}
	slices.Reverse(events)
	for range 20 {
		if again := computeAB(events, Options{}); again.Text() != first.Text() {
			t.Fatalf("Text() changed between runs:\n%s\nvs\n%s", first.Text(), again.Text())
		}
	}
}

// ratchet: test_removed TestComputeAB_AnArmIsDecidableAtThirtyLanes: the 30-lane rule is replaced by the stop rule, covered by the TestComputeAB tests in ab_decide_test.go

// ratchet: test_removed TestAB_TextNamesEachArmItsCountsAndWhetherItReachedThirtyLanes: renamed TestAB_TextNamesEachArmItsCountsAndTheMaximumLanes: arms no longer report reaching 30 lanes
