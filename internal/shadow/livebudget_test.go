package shadow

import (
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func repeat(d time.Duration, n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = d
	}
	return out
}

// The budget is p90 of the recorded decision times times the headroom, never under the
// floor and never over the cap; with too little history it is the floor.
func TestSizedBudget_IsTheP90TimesTheHeadroomBetweenTheFloorAndTheCap(t *testing.T) {
	floor := ms(50)
	for _, c := range []struct {
		name    string
		samples []time.Duration
		want    time.Duration
	}{
		{"no history is the floor", nil, ms(50)},
		{"one short of the samples needed is the floor", repeat(ms(100), BudgetMinSamples-1), ms(50)},
		{"the samples needed size it", repeat(ms(100), BudgetMinSamples), ms(300)},
		{"fast decisions never go under the floor", repeat(ms(5), BudgetMinSamples), ms(50)},
		{"slow decisions stop at the cap", repeat(ms(400), BudgetMinSamples), BudgetCap},
		{"a millisecond under the cap is not cut", repeat(ms(165), BudgetMinSamples), ms(495)},
		{"a millisecond over the cap is cut", repeat(ms(168), BudgetMinSamples), BudgetCap},
		{"p90 is the 18th of 20: two slow ones are not it", append(repeat(ms(10), 18), ms(900), ms(900)), ms(50)},
		{"p90 is the 18th of 20: three slow ones are it", append(repeat(ms(10), 17), ms(100), ms(100), ms(100)), ms(300)},
	} {
		if got := SizedBudget(floor, c.samples); got != c.want {
			t.Errorf("%s: SizedBudget = %v, want %v", c.name, got, c.want)
		}
	}
	if got := SizedBudget(time.Hour, repeat(ms(400), BudgetMinSamples)); got != time.Hour {
		t.Errorf("a floor above the cap = %v, want the floor kept (an explicit budget is never cut)", got)
	}
}

// A hook sizes its wait from the decisions it recorded, so a box that is slow answers
// inside a wait that fits it, and the decision it makes is recorded for the next.
func TestRedGreenLive_TheWaitIsSizedFromTheRecordedDecisionTimes(t *testing.T) {
	clk, _ := bjSetup(t)
	oldBudget := LiveBudget
	LiveBudget = ms(50)
	t.Cleanup(func() { LiveBudget = oldBudget })
	b := newShadowBox(t)
	dir := t.TempDir()
	b.world.StateDir = func(string) string { return dir }
	for range BudgetMinSamples {
		noteDecision(dir, ms(100))
	}
	b.world.Edits = func(string) []LedgerEdit { clk.Advance(ms(200)); return nil }
	res := b.liveAsk(livEnforce, "internal/lane/lane.go")
	if res.Overran || len(res.Asked) != 1 {
		t.Fatalf("result = %+v, want an answer: 200 ms fits the 300 ms the history sizes", res)
	}
	if res.Spent != ms(200) || res.Budget != ms(300) {
		t.Errorf("spent %v of %v, want 200ms of 300ms", res.Spent, res.Budget)
	}
	h := readDecisions(dir)
	if len(h) != BudgetMinSamples+1 || h[len(h)-1] != ms(200) {
		t.Errorf("history = %v, want the 200ms decision appended last", h)
	}
}

// A drop is evidence too: it says the work took at least the budget, so the next wait is
// larger, up to the cap.
func TestRedGreenLive_ADroppedDecisionIsRecordedAtTheBudgetItHit(t *testing.T) {
	clk, _ := bjSetup(t)
	oldBudget := LiveBudget
	LiveBudget = ms(50)
	t.Cleanup(func() { LiveBudget = oldBudget })
	b := newShadowBox(t)
	dir := t.TempDir()
	b.world.StateDir = func(string) string { return dir }
	b.world.Edits = func(string) []LedgerEdit { clk.Advance(ms(80)); return nil }
	res := b.liveAsk(livEnforce, "internal/lane/lane.go")
	if !res.Overran {
		t.Fatalf("result = %+v, want a drop", res)
	}
	if h := readDecisions(dir); len(h) != 1 || h[0] != ms(80) {
		t.Errorf("history = %v, want the 80ms the dropped decision had spent", h)
	}
}

// Only the most recent decisions size the wait: the file is bounded.
func TestNoteDecision_KeepsOnlyTheMostRecentWindow(t *testing.T) {
	dir := t.TempDir()
	for i := range BudgetWindow + 5 {
		noteDecision(dir, time.Duration(i+1)*time.Millisecond)
	}
	h := readDecisions(dir)
	if len(h) != BudgetWindow || h[0] != ms(6) || h[len(h)-1] != ms(BudgetWindow+5) {
		t.Errorf("history = %v (%d), want the last %d decisions, 6ms..%dms", h, len(h), BudgetWindow, BudgetWindow+5)
	}
	if got := readDecisions(""); got != nil {
		t.Errorf("history with no state dir = %v, want none", got)
	}
}

// A decision dropped for the budget is written with the lane, the file and the seconds
// it had spent, so a drop is a measured loss and not a silent one.
func TestRedGreenStepsLive_ADropNamesTheLaneTheFileAndTheSecondsSpent(t *testing.T) {
	clk, _ := bjSetup(t)
	oldBudget := LiveBudget
	LiveBudget = ms(50)
	t.Cleanup(func() { LiveBudget = oldBudget })
	b := newShadowBox(t)
	b.world.Edits = func(string) []LedgerEdit { clk.Advance(ms(80)); return nil }
	rel := "internal/lane/lane.go"
	live := b.liveAsk(livEnforce, rel)
	got := capture(t)
	src := Source{Root: b.root, Arm: "enforce", ArmWhy: "assigned", Mode: "enforce"}
	RecordFactsAnd(src, func() []Fact { return nil }, RedGreenStepsLive(b.world, src, Payload{SessionID: "s1", ToolName: "Edit"}, []string{b.file(rel)}, live))
	if len(*got) != 1 {
		t.Fatalf("%d events, want the one drop: %+v", len(*got), *got)
	}
	e, d := (*got)[0], (*got)[0].Detail
	if e.Lane != b.lane || d["cause"] != CauseBudget || d["file"] != b.file(rel) || d["spent_secs"] != "0.080" || d["budget_secs"] != "0.050" {
		t.Errorf("event = lane %q %v, want lane %q, cause budget, file %q, spent_secs 0.080 of budget_secs 0.050", e.Lane, d, b.lane, b.file(rel))
	}
}

// quietClock is a clock whose timers never fire: only the deadline check can tell that an
// answer came late.
type quietClock struct{ *bjClock }

func (quietClock) Timer(time.Duration) (<-chan time.Time, func()) { return nil, func() {} }

// An answer that arrives after the budget is as dropped as one that never arrives,
// whether or not the timer got to say so first.
func TestRedGreenLive_AnAnswerThatComesLateIsDroppedWithItsSeconds(t *testing.T) {
	inner := &bjClock{now: t0}
	old, oldBudget := clock, LiveBudget
	clock, LiveBudget = quietClock{inner}, ms(50)
	t.Cleanup(func() { clock, LiveBudget = old, oldBudget })
	b := newShadowBox(t)
	b.world.Edits = func(string) []LedgerEdit { inner.Advance(ms(80)); return nil }
	res := b.liveAsk(livEnforce, "internal/lane/lane.go")
	if !res.Overran || len(res.Asked) != 0 || res.Spent != ms(80) || res.Budget != ms(50) {
		t.Errorf("result = %+v, want a drop of the late answer: 80ms spent of 50ms", res)
	}
}
