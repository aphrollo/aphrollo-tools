package measure

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// shadowAt is one shadow event sec seconds after base. The fold reads the rule
// and the relation; the rest of the detail is for the replay.
func shadowAt(sec float64, lane, rule, relation string, kv ...string) tdd.Event {
	return ev(sec, lane, "shadow", detail(append([]string{"rule", rule, "relation", relation, "hook", "pretooluse"}, kv...)...))
}

func shadowRule(t *testing.T, s Shadow, rule string) ShadowRule {
	t.Helper()
	for _, r := range s.Rules {
		if r.Rule == rule {
			return r
		}
	}
	t.Fatalf("no row for rule %q in %+v", rule, s.Rules)
	return ShadowRule{}
}

func computeShadow(events []tdd.Event, o Options) Shadow {
	return ComputeShadow(events, base.Add(40*24*time.Hour), o)
}

func TestShadow_CountsEachRelationPerRule(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "deny-law-edit", "agree"),
		shadowAt(1, "a", "deny-law-edit", "agree"),
		shadowAt(2, "a", "deny-law-edit", "trellis-stricter"),
		shadowAt(3, "a", "deny-law-edit", "trellis-softer", "held_out", "true"),
		shadowAt(4, "a", "deny-law-edit", "trellis-softer"),
		shadowAt(5, "a", "rerun-suite", "trellis-softer"),
		shadowAt(6, "a", "not-tested", "verdict-mismatch"),
		ev(7, "a", "edit", nil),
	}
	s := computeShadow(events, Options{})
	got := shadowRule(t, s, "deny-law-edit")
	want := ShadowRule{Rule: "deny-law-edit", Fires: 5, Agree: 2, Stricter: 1, Softer: 2, SoftHeldOut: 1, HeldOut: 1, Open: 1}
	if got != want {
		t.Errorf("deny-law-edit = %+v, want %+v", got, want)
	}
	if r := shadowRule(t, s, "rerun-suite"); r.Softer != 1 || r.Fires != 1 {
		t.Errorf("rerun-suite = %+v, want one fire counted as trellis-softer", r)
	}
	if r := shadowRule(t, s, "not-tested"); r.Mismatch != 1 || r.Agree != 0 {
		t.Errorf("not-tested = %+v, want one verdict mismatch", r)
	}
	if s.Fires != 7 {
		t.Errorf("total fires = %d, want 7 (the edit event is not a shadow fire)", s.Fires)
	}
}

func TestShadow_WouldBeBlocksAreJudgedByWhatFollowsOnTheirLane(t *testing.T) {
	const r = "primary-write"
	st := func(sec float64, lane string) tdd.Event { return shadowAt(sec, lane, r, "trellis-stricter") }
	events := []tdd.Event{
		// wrong: an override within ten minutes, even though CI later went red
		st(0, "wrong"), overrideAt(120, 0, "wrong", "override-x"), ev(3600, "wrong", "ci", verdictDetail("red")),
		// a catch by each downstream authority
		st(0, "gate"), ev(3600, "gate", "commit_gate_result", verdictDetail("blocked")),
		st(0, "ci"), ev(3600, "ci", "ci", verdictDetail("red")),
		st(0, "esc"), ev(86400, "esc", "escape", verdictDetail("product")),
		// a pass: merged with nothing red after the fire
		st(0, "pass"), ev(3600, "pass", "merge", verdictDetail("ok")),
		// a catch before the merge still counts as a catch
		st(0, "both"), ev(60*30, "both", "ci", verdictDetail("red")), ev(7200, "both", "merge", verdictDetail("ok")),
		// open: nothing yet
		st(0, "open"),
		// an override after the window is not a wrong block: the lane stays open
		st(0, "late"), overrideAt(11*60, 0, "late", "override-x"),
		// an allowed narrowed rerun is no override
		st(0, "rerun"), overrideAt(30, 0, "rerun", "override-bash-narrowed"),
		// another lane's red is not this lane's
		st(0, "mine"), ev(60, "theirs", "ci", verdictDetail("red")),
		// a red from before the fire is not downstream of it
		ev(-60, "before", "ci", verdictDetail("red")), st(0, "before"),
		// a queued merge has not landed
		st(0, "queued"), ev(60, "queued", "merge", verdictDetail("queued")),
		// a red past the horizon is not this fire's catch
		st(0, "old"), ev(29*24*3600, "old", "ci", verdictDetail("red")),
	}
	got := shadowRule(t, computeShadow(events, Options{}), r)
	want := ShadowRule{Rule: r, Fires: 13, Stricter: 13, Catches: 4, Wrong: 1, Passes: 1, Open: 7}
	if got != want {
		t.Errorf("%s = %+v, want %+v", r, got, want)
	}
}

func TestShadow_ARateNeedsTenFiresAndSaysSoBelow(t *testing.T) {
	var nine, ten []tdd.Event
	for i := range 10 {
		rel := "agree"
		if i >= 8 {
			rel = "trellis-softer"
		}
		e := shadowAt(float64(i), "a", "warn-law", rel)
		ten = append(ten, e)
		if i < 9 {
			nine = append(nine, e)
		}
	}
	few := shadowRule(t, computeShadow(nine, Options{}), "warn-law")
	if few.Rate() != "" {
		t.Errorf("9 fires gave a rate %q, want none", few.Rate())
	}
	enough := shadowRule(t, computeShadow(ten, Options{}), "warn-law")
	if enough.Rate() != "80%" {
		t.Errorf("10 fires, 8 agreeing: rate = %q, want 80%%", enough.Rate())
	}
	text := computeShadow(nine, Options{}).Text()
	if !strings.Contains(text, "under 10 fires: no rate") || strings.Contains(text, "%") {
		t.Errorf("text for 9 fires:\n%s\nwant it to say there is no rate under 10 fires, and print no percentage", text)
	}
}

func TestShadow_WindowNarrowsFiresButNotWhatFollowsThem(t *testing.T) {
	const day = 24 * 3600
	events := []tdd.Event{
		shadowAt(0, "a", "r", "trellis-stricter"),            // 40 days before now: outside a 7-day window
		shadowAt(39*day, "a", "r", "trellis-stricter"),       // inside
		ev(39*day+60, "a", "ci", verdictDetail("red")),       // follows the inside fire
		overrideAt(39*day+30, 0, "a", "override-x"),          // within ten minutes: wrong beats catch
		shadowAt(39*day+3600, "c", "r", "agree"),             // inside
		shadowAt(39*day+3600, "c", "other", "agree"),         // inside, another rule
		shadowAt(0, "d", "outside-only", "trellis-stricter"), // outside
		ev(39*day+3600, "a", "merge", verdictDetail("ok")),   // after the fire, but wrong came first
		ev(0, "e", "merge", verdictDetail("ok")),             // another lane
		shadowAt(39*day+10, "e", "r", "trellis-stricter"),    // inside, and e merged before it
	}
	s := computeShadow(events, Options{Window: 7 * day * time.Second})
	got := shadowRule(t, s, "r")
	want := ShadowRule{Rule: "r", Fires: 3, Agree: 1, Stricter: 2, Wrong: 1, Open: 1}
	if got != want {
		t.Errorf("r = %+v, want %+v", got, want)
	}
	for _, r := range s.Rules {
		if r.Rule == "outside-only" {
			t.Errorf("a rule whose only fire is outside the window has a row: %+v", r)
		}
	}
}

func TestShadow_InputOrderAndLaneFilter(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "r", "trellis-stricter"), ev(60, "a", "ci", verdictDetail("red")),
		shadowAt(1, "b", "r", "agree"),
	}
	reversed := []tdd.Event{events[2], events[1], events[0]}
	if a, b := computeShadow(events, Options{}).Text(), computeShadow(reversed, Options{}).Text(); a != b {
		t.Errorf("order changed the text:\n%s\nvs\n%s", a, b)
	}
	if r := shadowRule(t, computeShadow(events, Options{Lane: "a"}), "r"); r.Fires != 1 || r.Catches != 1 {
		t.Errorf("lane a: %+v, want its one fire, caught", r)
	}
}

func TestShadow_TextPrintsTheWindowsAndEveryCount(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "rerun-suite", "trellis-softer"),
		shadowAt(1, "a", "primary-write", "trellis-stricter"), ev(60, "a", "ci", verdictDetail("red")),
		shadowAt(2, "a", "primary-write", "agree", "held_out", "true"),
	}
	want := strings.Join([]string{
		"shadow fires          3 (whole log)",
		"  wrong block         an override within 10 min of the fire on its lane",
		"  catch / pass        a later commit gate refusal, red CI or escape / a merge, within " + strconv.Itoa(ShadowHorizonDays) + " days",
		"primary-write          2 fires  agree 1  would-be block 1  softer 0 (0 held out)  mismatch 0  held out 1",
		"                       would-be blocks: catches 1  wrong 0  passes 0  open 0",
		"                       under 10 fires: no rate",
		"rerun-suite            1 fires  agree 0  would-be block 0  softer 1 (0 held out)  mismatch 0  held out 0",
		"                       under 10 fires: no rate",
		"",
	}, "\n")
	if got := computeShadow(events, Options{}).Text(); got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}
}

func TestExplain_AShadowEventSaysWhatFollowedItAndCountsItsRule(t *testing.T) {
	events := []tdd.Event{
		withSeq(1, shadowAt(0, "x", "primary-write", "trellis-stricter", "live_rule", "primary-checkout", "trellis", "block", "aphrollo", "allow", "key", "k9")),
		overrideAt(60, 2, "x", "override-x"),
		withSeq(3, shadowAt(100, "y", "primary-write", "agree", "held_out", "true")),
	}
	w := explain(t, events, 1)
	if w.ShadowFire == nil {
		t.Fatalf("no shadow replay for a shadow event: %+v", w)
	}
	f := w.ShadowFire
	if f.Rule != "primary-write" || f.Relation != "trellis-stricter" || f.Trellis != "block" || f.Aphrollo != "allow" || f.Outcome != ShadowWrong || f.Counts.Fires != 2 || f.Counts.Wrong != 1 {
		t.Errorf("replay = %+v, want primary-write, trellis-stricter, trellis block vs aphrollo allow, outcome %q, counts over 2 fires", f, ShadowWrong)
	}
	want := strings.Join([]string{
		"seq 1  shadow  2026-10-01T10:00:00.000Z  lane x",
		"rule        primary-write",
		"live rule   primary-checkout",
		"trellis     block",
		"aphrollo    allow",
		"relation    trellis-stricter",
		"held out    no",
		"key         k9",
		"outcome     wrong block: an override followed within 10 min",
		"this rule   2 fires: agree 1, would-be block 1, softer 0, mismatch 0, held out 1",
		"detail      aphrollo=allow hook=pretooluse key=k9 live_rule=primary-checkout relation=trellis-stricter rule=primary-write trellis=block",
		"",
	}, "\n")
	if got := w.Text(); got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}
	if f3 := explain(t, events, 3).ShadowFire; f3 == nil || f3.Outcome != ShadowNotABlock || !f3.HeldOut {
		t.Errorf("an agreeing held-out fire: %+v, want outcome %q and held out", f3, ShadowNotABlock)
	}
}

func TestExplain_ADenySaysHowManyShadowFiresItsLiveRuleHas(t *testing.T) {
	events := []tdd.Event{
		denyAt(0, 1, "x", "discard-bash"),
		shadowAt(1, "x", "discard-work", "agree", "live_rule", "discard-bash"),
		shadowAt(2, "x", "discard-work", "agree", "live_rule", "discard-bash"),
		shadowAt(3, "y", "discard-work", "trellis-stricter", "live_rule", "discard-bash"),
		shadowAt(4, "y", "primary-write", "agree", "live_rule", "primary-checkout"),
		denyAt(5, 2, "x", "tautology"),
	}
	if got := explain(t, events, 1).Shadow; got != "3 fires: agree 2, would-be block 1, softer 0, mismatch 0, held out 0" {
		t.Errorf("shadow of a deny of discard-bash = %q", got)
	}
	if got := explain(t, events, 2).Shadow; got != ShadowNoFires {
		t.Errorf("shadow of a deny no shadowed rule reads = %q, want %q", got, ShadowNoFires)
	}
}
