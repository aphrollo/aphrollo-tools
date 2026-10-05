package measure

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const lawLive = "ratchet:module_size"

// shadowAt is one shadow event sec seconds after base. The fold reads the rule,
// the relation and, for a would-be block, the live rule, the worktree and whether
// the fact was about the primary checkout; the rest is for the replay.
func shadowAt(sec float64, lane, rule, relation string, kv ...string) tdd.Event {
	return ev(sec, lane, "shadow", detail(append([]string{"rule", rule, "relation", relation, "hook", "pretooluse"}, kv...)...))
}

// blockAt is a would-be block of a deny law on lane: trellis denies where the
// hook only warned.
func blockAt(sec float64, lane string, kv ...string) tdd.Event {
	return shadowAt(sec, lane, "deny-law-edit", "trellis-stricter", append([]string{"live_rule", lawLive, "wt", "/w/" + lane}, kv...)...)
}

// escapeOf is an override of the module_size law.
func escapeOf(sec float64, lane string) tdd.Event {
	return ev(sec, lane, "override", detail("override", "smell-escape:"+lawLive))
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
		shadowAt(7, "a", "run-verdict", "not-comparable"),
		ev(8, "a", "edit", nil),
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
	if r := shadowRule(t, s, "run-verdict"); r.NotComparable != 1 || r.Mismatch != 0 {
		t.Errorf("run-verdict = %+v, want one fire counted as not comparable and none as a mismatch", r)
	}
	if s.Fires != 8 {
		t.Errorf("total fires = %d, want 8 (the edit event is not a shadow fire)", s.Fires)
	}
}

func TestShadow_WouldBeBlocksAreJudgedByWhatFollowsOnTheirLane(t *testing.T) {
	events := []tdd.Event{
		// wrong: an override of the same law within ten minutes, even though CI later went red
		blockAt(0, "wrong"), escapeOf(120, "wrong"), ev(3600, "wrong", "ci", verdictDetail("red")),
		// a catch by each downstream authority
		blockAt(0, "gate"), ev(3600, "gate", "commit_gate_result", verdictDetail("blocked")),
		blockAt(0, "ci"), ev(3600, "ci", "ci", verdictDetail("red")),
		blockAt(0, "esc"), ev(86400, "esc", "escape", verdictDetail("product")),
		// a pass: merged with nothing red after the fire
		blockAt(0, "pass"), ev(3600, "pass", "merge", verdictDetail("ok")),
		// a catch before the merge still counts as a catch
		blockAt(0, "both"), ev(60*30, "both", "ci", verdictDetail("red")), ev(7200, "both", "merge", verdictDetail("ok")),
		// open: nothing yet
		blockAt(0, "open"),
		// an override after the window is not a wrong block: the lane stays open
		blockAt(0, "late"), escapeOf(11*60, "late"),
		// an allowed narrowed rerun is no override
		blockAt(0, "rerun"), ev(30, "rerun", "override", detail("override", "override-bash-narrowed")),
		// another lane's red is not this lane's
		blockAt(0, "mine"), ev(60, "theirs", "ci", verdictDetail("red")),
		// a red from before the fire is not downstream of it
		ev(-60, "before", "ci", verdictDetail("red")), blockAt(0, "before"),
		// a queued merge has not landed
		blockAt(0, "queued"), ev(60, "queued", "merge", verdictDetail("queued")),
		// a red past the horizon is not this fire's catch
		blockAt(0, "old"), ev(29*24*3600, "old", "ci", verdictDetail("red")),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "deny-law-edit")
	want := ShadowRule{Rule: "deny-law-edit", Fires: 13, Stricter: 13, Catches: 4, Wrong: 1, Passes: 1, Open: 7}
	if got != want {
		t.Errorf("deny-law-edit = %+v, want %+v", got, want)
	}
}

// An override only makes a would-be block wrong when it waives the same rule: the
// action was allowed, so an override of that rule's own wall is the evidence.
func TestShadow_AnOverrideOfAnotherRuleIsNotAWrongBlock(t *testing.T) {
	events := []tdd.Event{
		blockAt(0, "other"), ev(60, "other", "override", detail("override", "smell-escape:tautology")),
		blockAt(0, "off"), ev(60, "off", "override", detail("override", "override-off")),
		blockAt(0, "same"), ev(60, "same", "override", detail("override", "smell-escape:module_size")),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "deny-law-edit")
	if got.Wrong != 1 || got.Open != 2 {
		t.Errorf("got %+v, want only the escape of the same law to be a wrong block", got)
	}
}

func TestOverrideAbout_NamesTheRulesOwnWall(t *testing.T) {
	over := func(name string) stamped {
		return stamped{Event: tdd.Event{Kind: "override", Detail: map[string]string{"override": name}}}
	}
	fire := func(rule, live string) stamped {
		return stamped{Event: tdd.Event{Kind: "shadow", Detail: map[string]string{"rule": rule, "live_rule": live}}}
	}
	cases := []struct {
		name string
		o    string
		f    stamped
		want bool
	}{
		{"discard wall", "override-discard-used", fire("discard-work", "discard-bash"), true},
		{"primary wall", "override-primary-edits-on", fire("primary-write", "primary-checkout"), true},
		{"bash suite wall", "override-bash-soak", fire("rerun-suite", "bash-whole-suite"), true},
		{"undercover", "override-undercover-allow", fire("attribution", "undercover"), true},
		{"direct PR", "override-direct-pr-open-allow", fire("bypass-verb", "direct-pr-open"), true},
		{"the law's own escape", "smell-escape:ratchet:module_size", fire("deny-law-edit", lawLive), true},
		{"discard override for a primary fire", "override-discard-used", fire("primary-write", "primary-checkout"), false},
		{"a smell escape for a discard", "smell-escape:tautology", fire("discard-work", "discard-bash"), false},
		{"another law", "smell-escape:ratchet:other_law", fire("deny-law-edit", lawLive), false},
		{"tdd off is no rule's wall", "override-off", fire("deny-law-edit", lawLive), false},
	}
	for _, c := range cases {
		if got := overrideAbout(over(c.o), c.f); got != c.want {
			t.Errorf("%s: overrideAbout(%q) = %v, want %v", c.name, c.o, got, c.want)
		}
	}
}

// A lane name used again after its merge is another lane, from its first edit,
// fire or lane.opened on: what happens to the second does not decide the first.
func TestShadow_AReusedLaneNameIsANewLaneAfterItsMerge(t *testing.T) {
	events := []tdd.Event{
		blockAt(0, "x"), ev(100, "x", "merge", verdictDetail("ok")),
		ev(150, "x", "edit", detail("edit", "e1")), // the lane is opened again
		ev(200, "x", "ci", verdictDetail("red")),   // the recreated lane's
		blockAt(300, "x"), ev(400, "x", "ci", verdictDetail("red")),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "deny-law-edit")
	want := ShadowRule{Rule: "deny-law-edit", Fires: 2, Stricter: 2, Passes: 1, Catches: 1}
	if got != want {
		t.Errorf("got %+v, want %+v: the first fire passed at its own lane's merge, the second was caught by its own red", got, want)
	}

	// A fire of the name, or its lane.opened, is the opening too.
	for name, opening := range map[string]tdd.Event{"a fire": blockAt(150, "x"), "lane.opened": ev(150, "x", "lane.opened", detail())} {
		evs := []tdd.Event{blockAt(0, "x"), ev(100, "x", "merge", verdictDetail("ok")), opening, ev(200, "x", "ci", verdictDetail("red"))}
		if r := shadowRule(t, computeShadow(evs, Options{}), "deny-law-edit"); r.Passes < 1 {
			t.Errorf("%s: %+v, want the first fire to pass: the red follows a new opening", name, r)
		}
	}
}

// What makes a catch is made after the merge by definition: an escape or a red CI
// run of the lane once it landed is a catch, within the horizon, and nothing
// arriving leaves the pass.
func TestShadow_AnEscapeOrRedCIAfterTheMergeIsStillACatch(t *testing.T) {
	merged := ev(100, "x", "merge", verdictDetail("ok"))
	for name, c := range map[string]struct {
		after []tdd.Event
		want  ShadowRule
	}{
		"an escape":                  {[]tdd.Event{ev(200, "x", "escape", verdictDetail("missed-in-ci"))}, ShadowRule{Catches: 1}},
		"a red CI run":               {[]tdd.Event{ev(200, "x", "ci", verdictDetail("red"))}, ShadowRule{Catches: 1}},
		"nothing":                    {nil, ShadowRule{Passes: 1}},
		"an escape past the horizon": {[]tdd.Event{ev(ShadowHorizonDays*24*3600+500, "x", "escape", verdictDetail("late"))}, ShadowRule{Passes: 1}},
		"another lane's escape":      {[]tdd.Event{ev(200, "y", "escape", verdictDetail("elsewhere"))}, ShadowRule{Passes: 1}},
	} {
		got := shadowRule(t, computeShadow(append([]tdd.Event{blockAt(0, "x"), merged}, c.after...), Options{}), "deny-law-edit")
		if got.Catches != c.want.Catches || got.Passes != c.want.Passes || got.Open != 0 || got.Wrong != 0 {
			t.Errorf("%s: %+v, want catches %d passes %d", name, got, c.want.Catches, c.want.Passes)
		}
	}
}

// The primary checkout has no lane of its own: whatever happens on main, in an
// outside merge or elsewhere, never turns a fire there into a pass or a catch.
func TestShadow_AFireInThePrimaryCheckoutStaysOpen(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "main", "primary-write", "trellis-stricter", "primary", "true", "wt", "/w/main"),
		ev(60, "main", "override", detail("override", "override-primary-edits-on")),
		ev(120, "main", "ci", verdictDetail("red")),
		ev(180, "main", "merge", verdictDetail("ok")),
		ev(240, "", "merge", verdictDetail("ok", "by", "outside")),
		// a fire of another rule on the trunk's own name is no better placed
		shadowAt(0, "main", "deny-law-edit", "trellis-stricter", "live_rule", lawLive),
		// a fire on no lane has nothing to join
		shadowAt(0, "", "deny-law-edit", "trellis-stricter", "live_rule", lawLive),
		ev(300, "", "ci", verdictDetail("red")),
	}
	s := computeShadow(events, Options{})
	if r := shadowRule(t, s, "primary-write"); r.Open != 1 || r.Catches+r.Wrong+r.Passes != 0 {
		t.Errorf("primary-write = %+v, want its fire open", r)
	}
	if r := shadowRule(t, s, "deny-law-edit"); r.Open != 2 || r.Catches+r.Wrong+r.Passes != 0 {
		t.Errorf("deny-law-edit on main or no lane = %+v, want both fires open", r)
	}
}

// ratchet: test_removed TestShadow_AnEventFromAnotherWorktreeOfTheNameDoesNotJoin: the log carries no worktree root to tell two checkouts of a name apart, so the lane's name bounded by its merge is the identity, proved by TestShadow_AReusedLaneNameIsANewLaneAfterItsMerge

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
		blockAt(0, "a"),      // 40 days before now: outside a 7-day window
		blockAt(39*day, "a"), // inside
		ev(39*day+60, "a", "ci", verdictDetail("red")),       // follows the inside fire
		escapeOf(39*day+30, "a"),                             // within ten minutes: wrong beats catch
		shadowAt(39*day+3600, "c", "deny-law-edit", "agree"), // inside
		shadowAt(39*day+3600, "c", "other", "agree"),         // inside, another rule
		shadowAt(0, "d", "outside-only", "trellis-stricter"), // outside
		ev(39*day+3600, "a", "merge", verdictDetail("ok")),   // after the fire, but wrong came first
		ev(0, "e", "merge", verdictDetail("ok")),             // before e's fire
		blockAt(39*day+10, "e"),                              // inside, and e merged before it
	}
	s := computeShadow(events, Options{Window: 7 * day * time.Second})
	got := shadowRule(t, s, "deny-law-edit")
	want := ShadowRule{Rule: "deny-law-edit", Fires: 3, Agree: 1, Stricter: 2, Wrong: 1, Open: 1}
	if got != want {
		t.Errorf("deny-law-edit = %+v, want %+v", got, want)
	}
	for _, r := range s.Rules {
		if r.Rule == "outside-only" {
			t.Errorf("a rule whose only fire is outside the window has a row: %+v", r)
		}
	}
}

func TestShadow_InputOrderAndLaneFilter(t *testing.T) {
	events := []tdd.Event{
		blockAt(0, "a"), ev(60, "a", "ci", verdictDetail("red")),
		shadowAt(1, "b", "deny-law-edit", "agree"),
	}
	reversed := []tdd.Event{events[2], events[1], events[0]}
	if a, b := computeShadow(events, Options{}).Text(), computeShadow(reversed, Options{}).Text(); a != b {
		t.Errorf("order changed the text:\n%s\nvs\n%s", a, b)
	}
	if r := shadowRule(t, computeShadow(events, Options{Lane: "a"}), "deny-law-edit"); r.Fires != 1 || r.Catches != 1 {
		t.Errorf("lane a: %+v, want its one fire, caught", r)
	}
}

func TestShadow_TextPrintsTheWindowsAndEveryCount(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "rerun-suite", "trellis-softer"),
		blockAt(1, "a"), ev(60, "a", "ci", verdictDetail("red")),
		shadowAt(2, "a", "deny-law-edit", "agree", "held_out", "true"),
	}
	want := strings.Join([]string{
		"shadow fires          3 (whole log)",
		"  note                " + shadowNotes[0],
		"  note                " + shadowNotes[1],
		"  note                " + shadowNotes[2],
		"  note                " + shadowNotes[3],
		"  note                " + shadowNotes[4],
		"  note                " + shadowNotes[5],
		"  wrong block         an override of the same rule within 10 min of the fire on its lane",
		"  catch / pass        a later commit gate refusal, red CI or escape / the lane's merge, within " + strconv.Itoa(ShadowHorizonDays) + " days",
		"deny-law-edit          2 fires  agree 1  would-be block 1  softer 0 (0 held out)  mismatch 0  not comparable 0  unjudged 0  held out 1",
		"                       would-be blocks: catches 1  wrong 0  passes 0  open 0",
		"                       under 10 fires: no rate",
		"rerun-suite            1 fires  agree 0  would-be block 0  softer 1 (0 held out)  mismatch 0  not comparable 0  unjudged 0  held out 0",
		"                       under 10 fires: no rate",
		"",
	}, "\n")
	if got := computeShadow(events, Options{}).Text(); got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}
}

func TestExplain_AShadowEventSaysWhatFollowedItAndCountsItsRule(t *testing.T) {
	events := []tdd.Event{
		withSeq(1, blockAt(0, "x", "trellis", "block", "aphrollo", "warn", "key", "k9")),
		withSeq(2, escapeOf(60, "x")),
		withSeq(3, shadowAt(100, "y", "deny-law-edit", "agree", "held_out", "true")),
	}
	w := explain(t, events, 1)
	if w.ShadowFire == nil {
		t.Fatalf("no shadow replay for a shadow event: %+v", w)
	}
	f := w.ShadowFire
	if f.Rule != "deny-law-edit" || f.Relation != "trellis-stricter" || f.Trellis != "block" || f.Aphrollo != "warn" || f.Outcome != ShadowWrong || f.Counts.Fires != 2 || f.Counts.Wrong != 1 {
		t.Errorf("replay = %+v, want deny-law-edit, trellis-stricter, trellis block vs aphrollo warn, outcome %q, counts over 2 fires", f, ShadowWrong)
	}
	want := strings.Join([]string{
		"seq 1  shadow  2026-10-01T10:00:00.000Z  lane x",
		"rule        deny-law-edit",
		"live rule   " + lawLive,
		"trellis     block",
		"aphrollo    warn",
		"relation    trellis-stricter",
		"held out    no",
		"key         k9",
		"outcome     wrong block: an override followed within 10 min",
		"this rule   2 fires: agree 1, would-be block 1, softer 0, mismatch 0, not comparable 0, held out 1",
		"detail      aphrollo=warn hook=pretooluse key=k9 live_rule=" + lawLive + " relation=trellis-stricter rule=deny-law-edit trellis=block wt=/w/x",
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
	if got := explain(t, events, 1).Shadow; got != "3 fires: agree 2, would-be block 1, softer 0, mismatch 0, not comparable 0, held out 0" {
		t.Errorf("shadow of a deny of discard-bash = %q", got)
	}
	if got := explain(t, events, 2).Shadow; got != ShadowNoFires {
		t.Errorf("shadow of a deny no shadowed rule reads = %q, want %q", got, ShadowNoFires)
	}
}

// The report says what its numbers are not: observation is one-sided, and the
// kernel runs on its defaults.
func TestShadow_SaysItIsOneSidedAndOnDefaultConfig(t *testing.T) {
	s := computeShadow([]tdd.Event{shadowAt(0, "a", "rerun-suite", "agree")}, Options{})
	text := s.Text()
	for _, want := range []string{
		"observed only where a hook acted",
		"agreement is overstated",
		"trellis acting alone is seen at a waived primary-checkout write and, for red-green, at each code edit",
		"default config, except red-green and stop-red",
		"red-green is observed",
		"escape comment is not logged as an override",
		"never as agreement",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text does not say %q:\n%s", want, text)
		}
	}
	if len(s.Notes) == 0 {
		t.Error("the notes are not in the JSON form")
	}
}

// ratchet: test_removed TestShadow_PlainRunAgreementsAreRunResultsMinusTheRecordedRuns: agreement is no longer derived from run.result, proved by TestShadow_RunAgreementCountsShadowEventsAndUnjudgedRunsNeverAgree

// The fold counts shadow events alone: a run result with no shadow event beside it
// is no agreement, and a run no verdict was made of is counted unjudged.
func TestShadow_RunAgreementCountsShadowEventsAndUnjudgedRunsNeverAgree(t *testing.T) {
	run := func(sec float64, relation string) tdd.Event {
		return ev(sec, "a", "shadow", detail("rule", "run-verdict", "relation", relation, "hook", "posttooluse-run"))
	}
	result := func(sec float64) tdd.Event { return ev(sec, "a", "run.result", verdictDetail("green")) }
	events := []tdd.Event{
		result(0), result(1), result(2), // no shadow event beside them: not counted
		run(3, "agree"), run(4, "agree"), run(5, "agree"), run(6, "verdict-mismatch"),
		run(7, "not-comparable"), run(8, "unjudged"), run(9, "unjudged"),
		shadowAt(10, "a", "rerun-suite", "trellis-softer"), // a PreToolUse record is not a run
	}
	s := computeShadow(events, Options{})
	got := shadowRule(t, s, "run-verdict")
	want := ShadowRule{Rule: "run-verdict", Fires: 7, Agree: 3, Mismatch: 1, NotComparable: 1, Unjudged: 2}
	if got != want {
		t.Errorf("run-verdict = %+v, want %+v", got, want)
	}
	if s.Fires != 8 {
		t.Errorf("total fires = %d, want 8 shadow events", s.Fires)
	}
	if !strings.Contains(s.Text(), "unjudged 2") {
		t.Errorf("text does not print the unjudged count:\n%s", s.Text())
	}

	// Results outside the window are not counted, and neither are runs.
	const day = 24 * 3600
	win := computeShadow([]tdd.Event{run(0, "agree"), run(39*day, "unjudged")}, Options{Window: 7 * day * time.Second})
	if r := shadowRule(t, win, "run-verdict"); r.Agree != 0 || r.Unjudged != 1 || r.Fires != 1 {
		t.Errorf("windowed run-verdict = %+v, want only the run inside the window", r)
	}
}
