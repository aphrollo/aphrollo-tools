package shadow

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// laneIn finds a lane name whose holdout arm for rule is want.
func laneIn(t *testing.T, rule string, want bool) string {
	t.Helper()
	r, ok := kernel.LookupRule(rule)
	if !ok {
		t.Fatalf("no kernel rule %q", rule)
	}
	for i := range 200 {
		lane := "lane/probe-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if kernel.InHoldout(lane, r) == want {
			return lane
		}
	}
	t.Fatalf("no lane with holdout %v for %s", want, rule)
	return ""
}

func TestJudge_RelationAndHoldoutPerRule(t *testing.T) {
	live := laneIn(t, "deny-law-edit", false)
	held := laneIn(t, "deny-law-edit", true)
	bypassHeld := laneIn(t, "bypass-verb", true)
	cases := []struct {
		name    string
		fact    Fact
		lane    string
		trellis string
		rel     Relation
		heldOut bool
	}{
		{"primary write blocked by both", PrimaryWrite(kernel.ToolWrite, Block), live, "block", Agree, false},
		{"primary write waived in aphrollo is a would-be block", PrimaryWrite(kernel.ToolBash, Allow), live, "block", TrellisStricter, false},
		{"discard blocked by both", Discard(Block), live, "block", Agree, false},
		{"attribution blocked by both", Attribution(Block), live, "block", Agree, false},
		{"deny law blocked by both", Law("ratchet:x", true, Block), live, "block", Agree, false},
		{"deny law in the holdout arm guides where aphrollo blocks", Law("ratchet:x", true, Block), held, "warn", TrellisSofter, true},
		{"deny law that aphrollo only warned of is a would-be block", Law("ratchet:x", true, Warn), live, "block", TrellisStricter, false},
		{"warn law warned by both", Law("ratchet:y", false, Warn), live, "guide", Agree, false},
		{"rerun denied by aphrollo is only guidance to trellis", Rerun(Block), live, "guide", TrellisSofter, false},
		{"outward call blocked by both", Outward("direct-pr", Block), live, "block", Agree, false},
		{"outward call in the holdout arm", Outward("direct-pr", Block), bypassHeld, "warn", TrellisSofter, true},
	}
	for _, c := range cases {
		got := Judge(c.fact, c.lane)
		if got.Trellis != c.trellis || got.Relation != c.rel || got.HeldOut != c.heldOut || got.Rule != c.fact.Rule {
			t.Errorf("%s: got trellis=%s relation=%s heldOut=%v rule=%s, want trellis=%s relation=%s heldOut=%v rule=%s",
				c.name, got.Trellis, got.Relation, got.HeldOut, got.Rule, c.trellis, c.rel, c.heldOut, c.fact.Rule)
		}
	}
}

// A shadowed rule is asked of the kernel with an empty lane State and no units.
// If a rule starts reading either, the same fact would decide differently under
// a lane's real state, and the data recorded here would be wrong.
func TestShadowedRules_ReadNoLaneOrUnitState(t *testing.T) {
	facts := map[string]Fact{
		"primary-write": PrimaryWrite(kernel.ToolWrite, Block),
		"discard-work":  Discard(Block),
		"attribution":   Attribution(Block),
		"deny-law-edit": Law("ratchet:x", true, Block),
		"warn-law":      Law("ratchet:y", false, Warn),
		"rerun-suite":   Rerun(Block),
		"bypass-verb":   Outward("direct-pr", Block),
	}
	states := []struct {
		st kernel.State
		us kernel.Units
	}{
		{kernel.State{}, nil},
		{kernel.State{Branch: "other", Life: kernel.LifeMerged, Head: "abc", CIRequired: []string{"windows"}}, nil},
		{kernel.State{Branch: kernel.TrunkLane, Life: kernel.LifeCIRed}, kernel.Units{"": {Phase: kernel.PhaseOpen, Test: "T"}}},
		{kernel.State{Life: kernel.LifeOpen}, kernel.Units{"u": {Phase: kernel.PhaseHeld, Hold: "e1"}, "v": {Phase: kernel.PhasePending, Test: "T"}}},
		{kernel.State{Life: kernel.LifeCommitted}, kernel.Units{"u": {Phase: kernel.PhaseClosed, Unproven: true}}},
	}
	lanes := []string{laneIn(t, "deny-law-edit", false), laneIn(t, "deny-law-edit", true)}
	for rule, f := range facts {
		for _, lane := range lanes {
			want := decide(f, lane, kernel.State{}, nil)
			if want.Rule != rule {
				t.Errorf("%s: the fact decides as rule %q", rule, want.Rule)
			}
			for i, s := range states {
				got := decide(f, lane, s.st, s.us)
				if got.Rule != want.Rule || got.Outcome != want.Outcome || got.Level != want.Level ||
					got.WouldDeny != want.WouldDeny || got.HeldOut != want.HeldOut {
					t.Errorf("%s lane %s state %d: decides %v, want %v: the rule reads lane or unit state", rule, lane, i,
						[]any{got.Rule, got.Outcome, got.Level, got.WouldDeny, got.HeldOut},
						[]any{want.Rule, want.Outcome, want.Level, want.WouldDeny, want.HeldOut})
				}
			}
		}
	}
}

func TestJudgeRun_GuidesAndMismatches(t *testing.T) {
	cases := []struct {
		name   string
		f      RunFact
		rule   string
		guide  string
		rel    Relation
		wantOK bool
	}{
		{"both green", RunFact{"green", kernel.VerdictGreen, ""}, "run-verdict", "", Agree, true},
		{"both red, aphrollo names the missing impl", RunFact{"red-missing-impl", kernel.VerdictRed, ""}, "run-verdict", "", Agree, true},
		{"a pass with warnings is green", RunFact{"green-with-warnings", kernel.VerdictGreen, ""}, "run-verdict", "", Agree, true},
		{"aphrollo calls an empty pass green, trellis did not test", RunFact{"writing-test", kernel.VerdictNotTested, "skipped"}, "not-tested", "not-tested", VerdictMismatch, true},
		{"both not tested for a timeout", RunFact{"timeout", kernel.VerdictNotTested, "timeout"}, "not-tested", "not-tested", Agree, true},
		{"not tested for different causes", RunFact{"infra-failed", kernel.VerdictNotTested, "skipped"}, "not-tested", "not-tested", VerdictMismatch, true},
		{"bogus red on both sides", RunFact{"red-bogus", kernel.VerdictRedBogus, ""}, "run-result", "red-bogus", Agree, true},
		{"aphrollo red, trellis bogus", RunFact{"red", kernel.VerdictRedBogus, ""}, "run-result", "red-bogus", VerdictMismatch, true},
		{"aphrollo bogus where the kernel input can only say red", RunFact{"red-bogus", kernel.VerdictRed, ""}, "run-verdict", "", NotComparable, true},
		{"a word that is no verdict is not recorded", RunFact{"queue-waiting", kernel.VerdictGreen, ""}, "", "", "", false},
	}
	for _, c := range cases {
		got, ok := JudgeRun(c.f, "lane/x")
		if ok != c.wantOK {
			t.Errorf("%s: recorded = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.Rule != c.rule || got.Guide != c.guide || got.Relation != c.rel {
			t.Errorf("%s: rule=%q guide=%q relation=%q, want %q %q %q", c.name, got.Rule, got.Guide, got.Relation, c.rule, c.guide, c.rel)
		}
	}
}

// The guide a run earns must not depend on the unit's state for the verdicts
// JudgeRun records a guide for: a run that did not test and a bogus red guide the
// same in every phase. A green or a real red guides by phase (passed at once,
// flaky), which a hook without a lane record cannot know, so JudgeRun records none.
func TestJudgeRun_GuideDoesNotDependOnUnitPhase(t *testing.T) {
	for _, v := range []kernel.Verdict{kernel.VerdictNotTested, kernel.VerdictRedBogus} {
		want := runGuides(kernel.Units{"run": {}}, v)
		for _, ph := range []kernel.Phase{kernel.PhaseClosed, kernel.PhasePending, kernel.PhaseOpen, kernel.PhaseHeld} {
			got := runGuides(kernel.Units{"run": {Phase: ph, Tree: "t"}}, v)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("verdict %s in phase %s guides %v, want %v as for an empty unit", v, ph, got, want)
			}
		}
	}
}

func runGuides(us kernel.Units, v kernel.Verdict) []string {
	d := kernel.Decide(kernel.State{}, us, kernel.Event{Kind: kernel.KindRunResult, Unit: "run", Tree: "t", Verdict: v, Cause: "skipped"}, kernel.Config{})
	var out []string
	for _, fx := range d.Effects {
		if fx.Kind == kernel.EffectGuide {
			out = append(out, fx.Detail)
		}
	}
	return out
}

// capture replaces the writer for one test and returns what was written.
func capture(t *testing.T) *[]core.Event {
	t.Helper()
	var mu sync.Mutex
	var got []core.Event
	old := appendEvent
	appendEvent = func(e core.Event) { mu.Lock(); got = append(got, e); mu.Unlock() }
	t.Cleanup(func() { appendEvent = old })
	return &got
}

func TestRecordFacts_WritesOneShadowEventWithMetadataOnly(t *testing.T) {
	got := capture(t)
	root := t.TempDir()
	RecordFacts(Source{Root: root, Actor: "s1/a1", Key: "k1"}, []Fact{Rerun(Block), Law("ratchet:x", false, Warn)})
	if len(*got) != 2 {
		t.Fatalf("wrote %d events, want 2", len(*got))
	}
	e := (*got)[0]
	want := map[string]string{"hook": "pretooluse", "rule": "rerun-suite", "live_rule": "bash-whole-suite", "trellis": "guide",
		"aphrollo": "block", "relation": "trellis-softer", "key": "k1", "wt": root}
	if e.Kind != "shadow" || e.Cmd != "" || e.Actor != "s1/a1" || !reflect.DeepEqual(e.Detail, want) {
		t.Errorf("event = kind %q cmd %q actor %q detail %v, want kind shadow, no cmd, detail %v", e.Kind, e.Cmd, e.Actor, e.Detail, want)
	}
}

func TestRecordRun_WritesTheClassesAndCauses(t *testing.T) {
	got := capture(t)
	root := t.TempDir()
	RecordRun(Source{Root: root, Key: "tree1"}, func() (RunFact, bool) {
		return RunFact{Word: "timeout", Verdict: kernel.VerdictNotTested, Cause: "timeout"}, true
	})
	if len(*got) != 1 {
		t.Fatalf("wrote %d events, want 1", len(*got))
	}
	want := map[string]string{"hook": "posttooluse-run", "rule": "not-tested", "trellis": "guide", "aphrollo": "timeout",
		"relation": "agree", "key": "tree1", "guide": "not-tested", "trellis_verdict": "not-tested", "aphrollo_verdict": "not-tested",
		"cause": "timeout", "aphrollo_cause": "timeout", "wt": root}
	if d := (*got)[0].Detail; !reflect.DeepEqual(d, want) {
		t.Errorf("detail = %v, want %v", d, want)
	}
}

// A record that cannot be written inside the budget is dropped: the hook goes on
// without waiting for it, and a panic in the writer never reaches it.
func TestRecordFacts_OverrunReturnsInsideTheBudgetAndPanicIsDropped(t *testing.T) {
	oldBudget, oldAppend := Budget, appendEvent
	t.Cleanup(func() { Budget, appendEvent = oldBudget, oldAppend })
	Budget = 20 * time.Millisecond
	release, started, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	appendEvent = func(core.Event) {
		close(started)
		<-release
		close(finished)
	}
	returned := make(chan struct{})
	go func() {
		RecordFacts(Source{Root: t.TempDir()}, []Fact{Discard(Block)})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("RecordFacts waited for a writer that outran the budget")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the writer never started")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the writer goroutine did not finish")
	}
	appendEvent = func(core.Event) { panic("writer failed") }
	RecordRun(Source{Root: t.TempDir()}, func() (RunFact, bool) { return RunFact{Word: "green", Verdict: kernel.VerdictGreen}, true })
}

// Only a fact about the primary checkout is filed as primary: its outcomes
// cannot be joined by a lane, and the fold leaves it open.
func TestRecordFacts_AFactAboutThePrimaryCheckoutIsFiledAsPrimary(t *testing.T) {
	got := capture(t)
	RecordFacts(Source{Root: t.TempDir()}, []Fact{PrimaryWrite(kernel.ToolWrite, Allow), Discard(Block)})
	if len(*got) != 2 {
		t.Fatalf("wrote %d events, want 2", len(*got))
	}
	if (*got)[0].Detail["primary"] != "true" {
		t.Errorf("a primary write is not filed as primary: %v", (*got)[0].Detail)
	}
	if v, ok := (*got)[1].Detail["primary"]; ok {
		t.Errorf("a discard is not about the primary checkout, but carries primary=%q", v)
	}
}
