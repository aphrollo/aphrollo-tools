package shadow

import (
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

var (
	livWarn    = kernel.Config{TDD: kernel.ModeWarn}
	livEnforce = kernel.Config{TDD: kernel.ModeEnforce}
	livOff     = kernel.Config{TDD: kernel.ModeOff}
)

func (b *shadowBox) liveAsk(cfg kernel.Config, rels ...string) LiveResult {
	b.t.Helper()
	files := make([]string, len(rels))
	for i, rel := range rels {
		files[i] = b.file(rel)
	}
	return b.world.RedGreenLive(Payload{SessionID: "s1", ToolName: "Edit"}, files, cfg)
}

// Under warn the kernel guides a code edit with no red and no cover, once per unit
// per lane, and never denies.
func TestRedGreenLive_UnderWarnTheKernelGuidesOnceAndNeverDenies(t *testing.T) {
	b := newShadowBox(t)
	res := b.liveAsk(livWarn, "internal/lane/lane.go")
	if res.Overran || len(res.Asked) != 1 {
		t.Fatalf("result = %+v, want one asked file", res)
	}
	a := res.Asked[0]
	if a.Decision.Outcome != kernel.OutcomeGuide || a.Decision.Rule != "red-green" || a.Unit != "internal/lane" || a.Lang != "go" {
		t.Errorf("asked = %+v, want a red-green guide for internal/lane (go)", a)
	}
	again := b.liveAsk(livWarn, "internal/lane/lane.go").Asked
	if len(again) != 1 || again[0].Decision.Outcome != kernel.OutcomeAllow {
		t.Errorf("second ask = %+v, want the one guidance line per unit per lane to be spent", again)
	}
}

// Under enforce it is a deny, every time, outside the holdout arm; in the holdout
// arm the kernel itself says guide.
func TestRedGreenLive_UnderEnforceTheKernelDeniesEveryTimeOutsideTheHoldoutArm(t *testing.T) {
	b := newShadowBox(t)
	for i := range 2 {
		got := b.liveAsk(livEnforce, "internal/lane/lane.go").Asked
		if len(got) != 1 || got[0].Decision.Outcome != kernel.OutcomeDeny || got[0].Decision.Override == "" {
			t.Fatalf("ask %d = %+v, want a deny that names an override", i, got)
		}
	}
	b.lane = laneIn(t, RuleRedGreen, true)
	held := b.liveAsk(livEnforce, "internal/lane/lane.go").Asked
	if len(held) != 1 || held[0].Decision.Outcome != kernel.OutcomeGuide || !held[0].Decision.HeldOut {
		t.Errorf("holdout lane = %+v, want the kernel's own shadowed guide", held)
	}
}

func TestRedGreenLive_UnderOffTheKernelSaysNothing(t *testing.T) {
	b := newShadowBox(t)
	got := b.liveAsk(livOff, "internal/lane/lane.go").Asked
	if len(got) != 1 || got[0].Decision.Outcome != kernel.OutcomeAllow {
		t.Errorf("asked = %+v, want an allow under tdd = off", got)
	}
}

func TestRedGreenLive_AnOpenRedOrACoveringGreenAllowsTheEdit(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	b.fold("a1", "j1", kernel.VerdictRed, "e1")
	if got := b.liveAsk(livEnforce, "internal/lane/lane.go").Asked; len(got) != 1 || got[0].Decision.Outcome != kernel.OutcomeAllow {
		t.Errorf("with the unit's red open: %+v, want an allow", got)
	}
}

// Only code files of a followed lane are asked: a test, a document and a trunk
// lane are not, as in the shadow record.
func TestRedGreenLive_AsksOnlyCodeFilesOfAFollowedLane(t *testing.T) {
	b := newShadowBox(t)
	if got := b.liveAsk(livEnforce, "internal/lane/lane_test.go", "README.md").Asked; len(got) != 0 {
		t.Errorf("a test file and a document were asked: %+v", got)
	}
	b.lane = "main"
	if got := b.liveAsk(livEnforce, "internal/lane/lane.go").Asked; len(got) != 0 {
		t.Errorf("a trunk lane was asked: %+v", got)
	}
}

// A question that costs more than the hook's own budget says nothing and is told so:
// the caller records it unjudged for the budget.
func TestRedGreenLive_AnAskThatOutrunsItsBudgetIsDroppedNotGuessed(t *testing.T) {
	clk, _ := bjSetup(t)
	oldBudget := LiveBudget
	LiveBudget = 50 * time.Millisecond
	t.Cleanup(func() { LiveBudget = oldBudget })
	b := newShadowBox(t)
	b.world.Edits = func(string) []LedgerEdit { clk.Advance(80 * time.Millisecond); return nil }
	res := b.liveAsk(livEnforce, "internal/lane/lane.go")
	if !res.Overran || len(res.Asked) != 0 {
		t.Errorf("result = %+v, want an overrun with nothing to say", res)
	}
}
