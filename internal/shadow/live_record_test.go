package shadow

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func (b *shadowBox) recordLive(src Source, live LiveResult, rel string) map[string]string {
	b.t.Helper()
	got := capture(b.t)
	src.Root = b.root
	files := []string{b.file(rel)}
	RecordFactsAnd(src, func() []Fact { return nil }, RedGreenStepsLive(b.world, src, Payload{SessionID: "s1", ToolName: "Edit"}, files, live))
	if len(*got) != 1 {
		b.t.Fatalf("%d events, want 1: %+v", len(*got), *got)
	}
	return (*got)[0].Detail
}

// The record of a decision the live hook made carries what the hook did and the arm
// the lane is in, so the A/B reads per arm from the log alone.
func TestRedGreenStepsLive_ARecordNamesWhatTheHookDidAndTheLanesArm(t *testing.T) {
	for _, c := range []struct {
		name, arm string
		cfg       kernel.Config
		aphrollo  string
	}{
		{"warn arm", "warn", livWarn, "warn"},
		{"enforce arm", "enforce", livEnforce, "block"},
	} {
		b := newShadowBox(t)
		live := b.liveAsk(c.cfg, "internal/lane/lane.go")
		d := b.recordLive(Source{Arm: c.arm, ArmWhy: "assigned", Mode: c.arm}, live, "internal/lane/lane.go")
		if d["rule"] != RuleRedGreen || d["aphrollo"] != c.aphrollo || d["trellis"] != "block" || d["arm"] != c.arm || d["arm_why"] != "assigned" || d["tdd"] != c.arm {
			t.Errorf("%s: record = %v, want aphrollo=%s, the kernel's block beside it, arm=%s assigned", c.name, d, c.aphrollo, c.arm)
		}
		if d["unit"] != "internal/lane" || d["lang"] != "go" {
			t.Errorf("%s: record = %v, want the unit and its language", c.name, d)
		}
	}
}

// A lane whose repo pins tdd is recorded with no arm, but with why.
func TestRedGreenStepsLive_APinnedLaneIsRecordedWithNoArm(t *testing.T) {
	b := newShadowBox(t)
	live := b.liveAsk(livEnforce, "internal/lane/lane.go")
	d := b.recordLive(Source{ArmWhy: "pinned", Mode: "enforce"}, live, "internal/lane/lane.go")
	if _, has := d["arm"]; has || d["arm_why"] != "pinned" || d["tdd"] != "enforce" {
		t.Errorf("record = %v, want why=pinned, tdd=enforce and no arm", d)
	}
}

// A call the live hook could not answer inside its budget is one unjudged record for
// the budget, carrying the arm, and the kernel is not asked again.
func TestRedGreenStepsLive_AnOverrunIsOneUnjudgedRecordForTheBudget(t *testing.T) {
	b := newShadowBox(t)
	d := b.recordLive(Source{Arm: "enforce", ArmWhy: "assigned", Mode: "enforce"}, LiveResult{Overran: true}, "internal/lane/lane.go")
	if d["relation"] != string(Unjudged) || d["cause"] != CauseBudget || d["rule"] != RuleRedGreen || d["arm"] != "enforce" {
		t.Errorf("record = %v, want an unjudged red-green record for the budget, in the enforce arm", d)
	}
}

// With tdd off nothing was asked live: the record is the shadow's own, aphrollo
// allowing, and names no arm.
func TestRedGreenStepsLive_WithNoLiveAnswerTheRecordIsTheShadowsOwn(t *testing.T) {
	b := newShadowBox(t)
	d := b.recordLive(Source{}, LiveResult{}, "internal/lane/lane.go")
	if d["aphrollo"] != "allow" || d["trellis"] != "block" {
		t.Errorf("record = %v, want aphrollo=allow beside the kernel's block", d)
	}
	for _, k := range []string{"arm", "arm_why", "tdd"} {
		if _, has := d[k]; has {
			t.Errorf("record = %v, want no %s", d, k)
		}
	}
}
