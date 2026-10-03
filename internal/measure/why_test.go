package measure

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func withSeq(n int64, e tdd.Event) tdd.Event {
	e.Seq = n
	return e
}

func denyAt(sec float64, seq int64, lane, rule string, kv ...string) tdd.Event {
	return withSeq(seq, ev(sec, lane, "deny", detail(append([]string{"rule", rule}, kv...)...)))
}

func overrideAt(sec float64, seq int64, lane, name string) tdd.Event {
	return withSeq(seq, ev(sec, lane, "override", detail("override", name)))
}

func editAt(sec float64, seq int64, lane string) tdd.Event {
	return withSeq(seq, ev(sec, lane, "edit", nil))
}

func explain(t *testing.T, events []tdd.Event, seq int64) Why {
	t.Helper()
	w, ok := Explain(events, seq)
	if !ok {
		t.Fatalf("seq %d not found in %d events", seq, len(events))
	}
	return w
}

func TestExplain_followUpOfADenyIsReadFromTheLaneAfterIt(t *testing.T) {
	cases := []struct {
		name        string
		events      []tdd.Event
		outcome     string
		wrongBlock  bool
		overrideSeq int64
		afterSecs   float64
	}{
		{"override 3 minutes later is a wrong block",
			[]tdd.Event{denyAt(0, 1, "a", "r"), overrideAt(180, 2, "a", "override-x")},
			OutcomeOverridden, true, 2, 180},
		{"override exactly at the window edge is still a wrong block",
			[]tdd.Event{denyAt(0, 1, "a", "r"), overrideAt(600, 2, "a", "override-x")},
			OutcomeOverridden, true, 2, 600},
		{"override 11 minutes later is not a wrong block",
			[]tdd.Event{denyAt(0, 1, "a", "r"), overrideAt(660, 2, "a", "override-x")},
			OutcomeOverriddenLate, false, 2, 660},
		{"an override on another lane is not this deny's",
			[]tdd.Event{denyAt(0, 1, "a", "r"), overrideAt(60, 2, "b", "override-x"), editAt(90, 3, "a")},
			OutcomeComplied, false, 0, 0},
		{"an edit after the deny is the agent moving on",
			[]tdd.Event{denyAt(0, 1, "a", "r"), editAt(30, 2, "a")},
			OutcomeComplied, false, 0, 0},
		{"a run result after the deny is the agent moving on",
			[]tdd.Event{denyAt(0, 1, "a", "r"), withSeq(2, ev(30, "a", "run.result", detail("result", "green")))},
			OutcomeComplied, false, 0, 0},
		{"the same rule denying again is a repeat, not compliance",
			[]tdd.Event{denyAt(0, 1, "a", "r"), denyAt(20, 2, "a", "r")},
			OutcomeRepeated, false, 0, 0},
		{"a different rule next is moving on",
			[]tdd.Event{denyAt(0, 1, "a", "r"), denyAt(20, 2, "a", "other")},
			OutcomeComplied, false, 0, 0},
		{"timing events do not count as the agent moving on",
			[]tdd.Event{denyAt(0, 1, "a", "r"), withSeq(2, ev(5, "a", "hook.timing", nil)), withSeq(3, ev(6, "a", "stage.timing", nil))},
			OutcomeNothingAfter, false, 0, 0},
		{"nothing recorded after the deny",
			[]tdd.Event{denyAt(0, 1, "a", "r")},
			OutcomeNothingAfter, false, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := explain(t, c.events, 1).Deny
			if d == nil {
				t.Fatal("a deny has a Deny section")
			}
			if d.Outcome != c.outcome || d.WrongBlock != c.wrongBlock {
				t.Errorf("outcome %q wrong block %v, want %q %v", d.Outcome, d.WrongBlock, c.outcome, c.wrongBlock)
			}
			if c.overrideSeq == 0 {
				if d.Override != nil {
					t.Errorf("no override follows, got %+v", d.Override)
				}
				return
			}
			if d.Override == nil || d.Override.Seq != c.overrideSeq || d.Override.AfterSecs != c.afterSecs || d.Override.Name != "override-x" {
				t.Errorf("override = %+v, want seq %d name override-x after %vs", d.Override, c.overrideSeq, c.afterSecs)
			}
		})
	}
}

func TestExplain_denyCarriesWhatTheLogRecordedAndCountsItsRule(t *testing.T) {
	events := []tdd.Event{
		denyAt(0, 1, "a", "ratchet:module_size", "cause", "law", "override", "law-escape-comment"),
		overrideAt(100, 2, "a", "override-x"),
		denyAt(1000, 3, "a", "ratchet:module_size"),
		editAt(1010, 4, "a"),
		overrideAt(5000, 5, "a", "override-y"),
		denyAt(2000, 6, "b", "ratchet:module_size"),
		denyAt(2100, 7, "b", "other-rule"),
	}
	d := explain(t, events, 1).Deny
	if d.Rule != "ratchet:module_size" || d.Cause != "law" || d.OfferedOverride != "law-escape-comment" {
		t.Errorf("rule/cause/offered = %q %q %q", d.Rule, d.Cause, d.OfferedOverride)
	}
	// Denies 1, 3 and 6 are the rule's. Override 2 follows deny 1 and override 5
	// follows deny 3 (the lane's latest), only the first within 10 minutes.
	// Denies 3 and 6 were followed by an edit and by another rule: complied.
	want := RuleCounts{Denies: 3, Overrides: 2, WrongBlocks: 1, Complied: 2}
	if d.Counts != want {
		t.Errorf("counts = %+v, want %+v", d.Counts, want)
	}
}

func TestExplain_ruleCountsAddUpToTheStatsFold(t *testing.T) {
	events := []tdd.Event{
		denyAt(0, 1, "a", "r1"), overrideAt(60, 2, "a", "o"),
		denyAt(100, 3, "b", "r2"), denyAt(130, 4, "a", "r2"),
		overrideAt(200, 5, "a", "o"), overrideAt(5000, 6, "b", "o"),
		denyAt(9000, 7, "a", "r1"), editAt(9001, 8, "a"),
		overrideAt(9100, 9, "c", "o"),
	}
	var overrides, wrong int
	for _, first := range []int64{1, 3} { // the first deny of r1 and of r2
		c := explain(t, events, first).Deny.Counts
		overrides += c.Overrides
		wrong += c.WrongBlocks
	}
	got := compute(events, Options{}).Denies
	// override 9 follows no deny on its lane: Compute counts it, no rule owns it
	if overrides != got.Overrides-1 || wrong != got.WrongBlocks {
		t.Errorf("rule counts sum to %d overrides, %d wrong blocks; Compute has %d and %d", overrides, wrong, got.Overrides, got.WrongBlocks)
	}
}

func TestExplain_ruleIsReadFromTheVerdictWhenTheDetailHasNone(t *testing.T) {
	e := withSeq(1, ev(0, "a", "deny", func(e *tdd.Event) { e.Verdict = "pretooluse-denied:disabled-test" }))
	d := explain(t, []tdd.Event{e}, 1).Deny
	if d.Rule != "disabled-test" || d.Cause != "" || d.OfferedOverride != "" {
		t.Errorf("rule/cause/offered = %q %q %q, want disabled-test and nothing else", d.Rule, d.Cause, d.OfferedOverride)
	}
}

// refHeldOut is the §5 hash written out again, so the test does not ask the
// code under test for its own expectation.
func refHeldOut(lane, rule string) bool {
	h := fnv.New32a()
	h.Write([]byte(lane + "\x00" + rule))
	return h.Sum32()%10 == 0
}

func armLanes(rule string) (held, live string) {
	for i := 0; held == "" || live == ""; i++ {
		lane := fmt.Sprintf("arm-%d", i)
		if refHeldOut(lane, rule) {
			held = lane
		} else {
			live = lane
		}
	}
	return held, live
}

func TestExplain_kernelRowAndHoldoutArmWhenTheTableHasTheRule(t *testing.T) {
	held, live := armLanes("commit-proof")
	for lane, wantArm := range map[string]bool{held: true, live: false} {
		k := explain(t, []tdd.Event{denyAt(0, 1, lane, "commit-proof")}, 1).Deny.Kernel
		if k == nil || k.Level != "enforce" || k.Section != "§4 pre-commit, §5 earned" || k.Class != "earned" || !k.Shadowable || k.InHoldout != wantArm {
			t.Errorf("lane %q: kernel = %+v, want enforce, §4 pre-commit, §5 earned, earned, shadowable, holdout %v", lane, k, wantArm)
		}
	}
	wallLane, _ := armLanes("primary-write")
	wall := explain(t, []tdd.Event{denyAt(0, 1, wallLane, "primary-write")}, 1).Deny.Kernel
	if wall == nil || wall.Shadowable || wall.InHoldout || wall.Class != "wall" {
		t.Errorf("a wall is never shadowed: kernel = %+v", wall)
	}
	if k := explain(t, []tdd.Event{denyAt(0, 1, held, "ratchet:module_size")}, 1).Deny.Kernel; k != nil {
		t.Errorf("a v1 rule the kernel table does not hold has no kernel row, got %+v", k)
	}
}

func ptr(v float64) *float64 { return &v }

func TestExplain_runResultNamesVerdictCauseTreeAndLatency(t *testing.T) {
	cases := []struct {
		name string
		e    tdd.Event
		want RunWhy
	}{
		{"a real red",
			withSeq(1, ev(0, "a", "run.result", verdictDetail("red", "result", "red", "edit", "e1", "latency_ms", "1241", "tree", "abc123"))),
			RunWhy{Verdict: "red", Result: "red", Edit: "e1", Tree: "abc123", LatencyMs: ptr(1241)}},
		{"a run that proved nothing",
			withSeq(1, ev(0, "a", "run.result", verdictDetail("deferred", "result", "not-tested", "cause", "deferred"))),
			RunWhy{Verdict: "deferred", Result: "not-tested", Cause: "deferred", NotTested: true}},
		{"latency that is not a number is not recorded",
			withSeq(1, ev(0, "a", "run.result", verdictDetail("green", "result", "green", "latency_ms", "soon"))),
			RunWhy{Verdict: "green", Result: "green"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := explain(t, []tdd.Event{c.e}, 1)
			if got.Run == nil || got.Deny != nil || !reflect.DeepEqual(*got.Run, c.want) {
				t.Errorf("run = %+v, want %+v", got.Run, c.want)
			}
		})
	}
}

func TestExplain_seqNotFoundAndOtherKinds(t *testing.T) {
	events := []tdd.Event{denyAt(0, 1, "a", "r"), withSeq(2, ev(1, "a", "edit", nil))}
	if _, ok := Explain(events, 99); ok {
		t.Error("seq 99 is in no event but Explain found it")
	}
	w := explain(t, events, 2)
	if w.Kind != "edit" || w.Deny != nil || w.Run != nil {
		t.Errorf("an edit is neither a deny nor a verdict: %+v", w)
	}
}

func TestExplain_inputOrderDoesNotChangeTheAnswer(t *testing.T) {
	events := []tdd.Event{
		denyAt(0, 1, "a", "r"), overrideAt(60, 2, "a", "o"), denyAt(100, 3, "a", "r"), editAt(110, 4, "a"),
	}
	want := explain(t, events, 1)
	reversed := []tdd.Event{events[3], events[2], events[1], events[0]}
	if got := explain(t, reversed, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("reversed log answers %+v, want %+v", got, want)
	}
}

func TestExplain_textIsLosslessAndSaysShadowIsNotRecorded(t *testing.T) {
	events := []tdd.Event{
		denyAt(0, 7, "lane/x", "ratchet:module_size", "cause", "law", "override", "law-escape-comment"),
		overrideAt(95, 8, "lane/x", "override-x"),
		withSeq(9, ev(200, "lane/x", "run.result", verdictDetail("timeout", "result", "not-tested", "cause", "timeout", "edit", "e9", "latency_ms", "425341"))),
	}
	events[0].Stage, events[0].Verdict = "preedit", "pretooluse-denied:ratchet:module_size"
	wantDeny := strings.Join([]string{
		"seq 7  deny  2026-10-01T10:00:00.000Z  lane lane/x",
		"rule        ratchet:module_size",
		"cause       law",
		"offered     law-escape-comment",
		"stage       preedit",
		"verdict     pretooluse-denied:ratchet:module_size",
		"detail      cause=law override=law-escape-comment rule=ratchet:module_size",
		"override    override-x (seq 8) 1m35s after: a wrong block, within 10 min",
		"outcome     overridden",
		"this rule   1 denies, 1 overrides, 1 wrong blocks, 0 complied",
		"kernel      no row for this rule in the rule table",
		"shadow      not recorded yet",
		"",
	}, "\n")
	if got := explain(t, events, 7).Text(); got != wantDeny {
		t.Errorf("deny text:\n%s\nwant:\n%s", got, wantDeny)
	}
	wantRun := strings.Join([]string{
		"seq 9  run.result  2026-10-01T10:03:20.000Z  lane lane/x",
		"verdict     timeout",
		"result      not-tested",
		"not tested  timeout",
		"tree        not recorded",
		"edit        e9",
		"latency     425341 ms from edit to verdict",
		"detail      cause=timeout edit=e9 latency_ms=425341 result=not-tested",
		"",
	}, "\n")
	if got := explain(t, events, 9).Text(); got != wantRun {
		t.Errorf("run text:\n%s\nwant:\n%s", got, wantRun)
	}
}

func TestExplain_textNamesTheKernelRowAndTheArm(t *testing.T) {
	held, live := armLanes("commit-proof")
	for lane, want := range map[string]string{
		held: "holdout     this lane is in the shadow arm",
		live: "holdout     this lane is not in the shadow arm",
	} {
		got := explain(t, []tdd.Event{denyAt(0, 1, lane, "commit-proof")}, 1).Text()
		for _, line := range []string{
			"kernel      commit-proof: level enforce, class earned, §4 pre-commit, §5 earned",
			want,
			"override    none within 10 min",
			"outcome     nothing recorded after the deny",
		} {
			if !strings.Contains(got, line+"\n") {
				t.Errorf("lane %q: text has no line %q:\n%s", lane, line, got)
			}
		}
	}
	wallLane, _ := armLanes("primary-write")
	got := explain(t, []tdd.Event{denyAt(0, 1, wallLane, "primary-write")}, 1).Text()
	if !strings.Contains(got, "holdout     never shadowed (class wall)\n") {
		t.Errorf("a wall is never shadowed, text:\n%s", got)
	}
}

func TestExplain_textWritesALongLatencyInFullDigits(t *testing.T) {
	e := withSeq(1, ev(0, "a", "run.result", verdictDetail("green", "result", "green", "latency_ms", "1500000")))
	if got := explain(t, []tdd.Event{e}, 1).Text(); !strings.Contains(got, "latency     1500000 ms from edit to verdict\n") {
		t.Errorf("latency of 1500000 ms not written in full digits:\n%s", got)
	}
}
