package kernel

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestStepUnit_testedCodeNeedsCoverageAndNoNewSymbol(t *testing.T) {
	cases := []struct {
		name string
		opts []func(*Event)
		want []string
	}{
		{"covered, adds nothing", []func(*Event){covered}, nil},
		{"covered but adds an exported symbol or function", []func(*Event){covered, addsSymbol}, []string{GuideUntestedCode}},
		{"no passing test executes it", nil, []string{GuideUntestedCode}},
	}
	for _, c := range cases {
		opts := append([]func(*Event){inFile(ClassCode), onTree("t2")}, c.opts...)
		_, fx := StepUnit(unitIn(PhaseClosed), ue(KindEdit, opts...))
		if !slices.Equal(guides(fx), c.want) {
			t.Errorf("%s: guides %v, want %v", c.name, guides(fx), c.want)
		}
	}
}

func TestStepUnit_guidanceComesOncePerUnitPerLaneAndEnforceMarksTheDeny(t *testing.T) {
	u, fx := StepUnit(unitIn(PhaseClosed), ue(KindEdit, inFile(ClassCode), onTree("t2")))
	if len(guides(fx)) != 1 || slices.ContainsFunc(fx, func(f Effect) bool { return f.WouldDeny }) {
		t.Fatalf("first edit under warn: %+v, want one guide that denies nothing", fx)
	}
	u, fx = StepUnit(u, ue(KindEdit, inFile(ClassCode), onTree("t3")))
	if len(guides(fx)) != 0 {
		t.Fatalf("second edit under warn guides %v, want none: once per unit per lane", guides(fx))
	}
	_, fx = StepUnit(u, ue(KindEdit, inFile(ClassCode), onTree("t4"), inMode(ModeEnforce)))
	if !slices.ContainsFunc(fx, func(f Effect) bool { return f.Kind == EffectGuide && f.Detail == GuideUntestedCode && f.WouldDeny }) {
		t.Fatalf("code edit under enforce: %+v, want the guide marked WouldDeny", fx)
	}
}

func TestStepUnit_aHoldGuidesOnceAndNeverMarksADenyAtAnyLevel(t *testing.T) {
	u, fx := StepUnit(unitIn(PhaseHeld), ue(KindEdit, inFile(ClassCode), onTree("t2"), inMode(ModeEnforce)))
	if slices.ContainsFunc(fx, func(f Effect) bool { return f.WouldDeny }) || !slices.Equal(guides(fx), []string{GuideHeld}) {
		t.Fatalf("first edit under a hold: %+v, want [%s] that denies nothing", fx, GuideHeld)
	}
	_, fx = StepUnit(u, ue(KindEdit, inFile(ClassCode), onTree("t3"), inMode(ModeEnforce)))
	if len(guides(fx)) != 0 {
		t.Fatalf("second edit under a hold guides %v, want none: once per unit per lane", guides(fx))
	}
}

func TestStepUnit_offFreezesTheMachine(t *testing.T) {
	for _, e := range []Event{
		ue(KindEdit, inFile(ClassTest), forTest("TestA"), onTree("t2")),
		result(VerdictRed, forTest("TestA")),
		trunkEscape("TestA", "esc-9"),
	} {
		e.Mode = ModeOff
		u := unitIn(PhaseClosed)
		got, fx := StepUnit(u, e)
		if !reflect.DeepEqual(got, u) || len(fx) != 0 {
			t.Errorf("%s under off: %+v %v, want the unit untouched and no effects", e.Kind, got, fx)
		}
	}
}

func TestStepUnit_anUnknownModeReadsAsWarnNeverAsOff(t *testing.T) {
	got, _ := StepUnit(unitIn(PhaseClosed), ue(KindEdit, inFile(ClassTest), forTest("TestA"), onTree("t2"), inMode("loud")))
	if got.Phase != PhasePending {
		t.Fatalf("unknown mode froze the machine: phase %q, want pending", got.Phase)
	}
}

func TestStepUnit_editsOfTestAndCodeAndUnparsedWritesRequestARunButOtherFilesDoNot(t *testing.T) {
	cases := []struct {
		file FileClass
		want bool
	}{{ClassTest, true}, {ClassCode, true}, {"", true}, {ClassOther, false}}
	for _, c := range cases {
		got, fx := StepUnit(unitIn(PhaseClosed), ue(KindEdit, inFile(c.file), onTree("t2"), covered))
		if hasKind(fx, EffectRequestRun) != c.want {
			t.Errorf("edit of class %q: effects %+v, want a run request = %v", c.file, fx, c.want)
		}
		if got.Tree != "t2" {
			t.Errorf("edit of class %q left the tree at %q, want t2", c.file, got.Tree)
		}
	}
	_, fx := StepUnit(unitIn(PhaseClosed), ue(KindEdit, inFile(ClassCode), onTree("t2"), covered))
	if len(fx) != 1 || fx[0].Kind != EffectRequestRun || fx[0].Tree != "t2" || fx[0].Unit != "pkg/a" {
		t.Fatalf("effects = %+v, want one run request for pkg/a at t2", fx)
	}
}

func TestStepUnit_aWriteNobodyParsedMarksTheUnitUnprovenUntilTheNextRealRun(t *testing.T) {
	u, _ := StepUnit(unitIn(PhaseClosed), ue(KindEdit, onTree("t2")))
	if !u.Unproven || u.Phase != PhaseClosed {
		t.Fatalf("unparsed write: %+v, want closed and unproven", u)
	}
	u, _ = StepUnit(u, result(VerdictNotTested, withCause(CauseTimeout), onTree("t2")))
	if !u.Unproven {
		t.Fatal("a not-tested result cleared unproven")
	}
	u, _ = StepUnit(u, result(VerdictGreen, onTree("t2")))
	if u.Unproven {
		t.Fatal("a real green left the unit unproven")
	}
}

func TestStepUnit_aRedBogusIsNotARealRun(t *testing.T) {
	u := unitIn(PhaseClosed)
	u.Unproven = true
	got, _ := StepUnit(u, result(VerdictRedBogus, withCause("build failed")))
	if !got.Unproven || got.LastReal != "" {
		t.Fatalf("red-bogus: %+v, want unproven kept and no last real", got)
	}
}

func TestStepUnit_theLastRealVerdictStandsThroughNotTested(t *testing.T) {
	u, _ := StepUnit(unitIn(PhaseClosed), result(VerdictGreen))
	if u.LastReal != VerdictGreen || u.LastRealTree != "t1" {
		t.Fatalf("after green: last real %q @%q, want green @t1", u.LastReal, u.LastRealTree)
	}
	u, _ = StepUnit(u, result(VerdictNotTested, withCause(CauseTimeout)))
	if u.LastReal != VerdictGreen || u.LastRealTree != "t1" {
		t.Fatalf("after not tested: last real %q @%q, want green @t1 standing", u.LastReal, u.LastRealTree)
	}
	u, _ = StepUnit(u, result(VerdictRedMissingImpl, forTest("TestA")))
	if u.LastReal != VerdictRed {
		t.Fatalf("after red-missing-impl: last real %q, want red", u.LastReal)
	}
}

func TestStepUnit_aGatedCommitSeedsLastRealOnlyWhereThereIsNone(t *testing.T) {
	seed := ue(KindCommitGated, onTree("base"))
	u, _ := StepUnit(Unit{}, seed)
	if u.LastReal != VerdictGreen || u.LastRealTree != "base" {
		t.Fatalf("seeded unit: %+v, want green @base", u)
	}
	u.LastReal, u.LastRealTree = VerdictRed, "t5"
	u, _ = StepUnit(u, seed)
	if u.LastReal != VerdictRed || u.LastRealTree != "t5" {
		t.Fatalf("a gated note overwrote a real verdict: %+v", u)
	}
	if u.Phase != "" {
		t.Fatalf("a gated commit moved the phase to %q", u.Phase)
	}
}

func TestStepUnit_aRunRequestIsRememberedUntilItsResultComesBack(t *testing.T) {
	u, _ := StepUnit(unitIn(PhaseClosed), ue(KindRunRequested))
	if u.Requested != "t1" {
		t.Fatalf("requested = %q, want t1", u.Requested)
	}
	u, _ = StepUnit(u, result(VerdictNotTested, withCause(CauseDeferred)))
	if u.Requested != "t1" {
		t.Fatalf("a deferred result cleared the request: %q: pending is not an answer", u.Requested)
	}
	u, _ = StepUnit(u, result(VerdictGreen))
	if u.Requested != "" {
		t.Fatalf("requested = %q after the result, want cleared", u.Requested)
	}
}

func TestStepUnit_aRetriedResultOfTheSameJobIsDropped(t *testing.T) {
	r := result(VerdictRed, forTest("TestA"), onJob("j42"))
	once, _ := StepUnit(unitIn(PhaseClosed), r)
	if once.LastJob != "j42" {
		t.Fatalf("last job = %q, want j42", once.LastJob)
	}
	moved := once
	moved.Phase, moved.Test = PhaseClosed, "" // the unit moved on; the retry must not drag it back
	again, fx := StepUnit(moved, r)
	if !reflect.DeepEqual(again, moved) || len(fx) != 0 {
		t.Fatalf("retried job j42: %+v %v, want no change and no effects", again, fx)
	}
}

func TestStepUnit_aCITestRedOnAGatedHeadOpensTheNamedTest(t *testing.T) {
	u, fx := StepUnit(unitIn(PhaseClosed), ciRed(FailureTest, true, forTest("TestCI")))
	if u.Phase != PhaseOpen || u.Test != "TestCI" || u.Earns || len(fx) != 0 {
		t.Fatalf("CI test red: %+v %v, want open(TestCI) that earns no pair", u, fx)
	}
}

func TestStepUnit_otherCIRedsHoldNothing(t *testing.T) {
	for _, e := range []Event{
		ciRed(FailureTest, false, forTest("TestCI")),
		ciRed(FailureMutation, true, forTest("TestCI")),
		ciRed(FailureCanary, true, forTest("TestCI")),
		ciRed(FailureTimeout, true),
		ciRed(FailureFlaky, true, forTest("TestCI")),
	} {
		u := unitIn(PhaseClosed)
		got, fx := StepUnit(u, e)
		if !reflect.DeepEqual(got, u) || len(fx) != 0 {
			t.Errorf("CI red %q gated=%v: %+v %v, want nothing", e.Failure, e.Gated, got, fx)
		}
	}
}

func TestStepUnit_aCIGreenOfTheNamedTestOnALaterHeadClosesAnEscapeOpenedUnit(t *testing.T) {
	u, _ := StepUnit(unitIn(PhaseClosed), ciRed(FailureTest, true, forTest("TestCI"), func(e *Event) { e.Head = "h1" }))
	if got, _ := StepUnit(u, ciGreenOf("TestCI", "h1")); got.Phase != PhaseOpen {
		t.Errorf("CI green on the red's own head closed the unit: it is not a later head")
	}
	if got, _ := StepUnit(u, ciGreenOf("TestOther", "h2")); got.Phase != PhaseOpen {
		t.Errorf("CI green of another test closed the unit")
	}
	got, _ := StepUnit(u, ciGreenOf("TestCI", "h2"))
	if got.Phase != PhaseClosed || got.Pair != (Pair{}) {
		t.Errorf("CI green of TestCI on h2: %+v, want closed with no pair", got)
	}
}

func TestStepUnit_aLocalGreenAtTheTreeCIFailedOnStillClosesAnEscape(t *testing.T) {
	u, _ := StepUnit(unitIn(PhaseClosed), ciRed(FailureTest, true, forTest("TestCI")))
	u, _ = StepUnit(u, result(VerdictGreen))
	if u.Phase != PhaseClosed {
		t.Fatalf("local green after a CI red: phase %q, want closed: a red that reproduces only elsewhere must not strand the box", u.Phase)
	}
}

func TestStepUnit_aHoldIsReleasedByTheReproducingRedTheLaterCIGreenOrTheClosingMerge(t *testing.T) {
	held, _ := StepUnit(unitIn(PhaseClosed), trunkEscape("TestA", "esc-9"))
	if held.Phase != PhaseHeld || held.Hold != "esc-9" || held.Test != "TestA" {
		t.Fatalf("trunk escape: %+v, want held by esc-9 on TestA", held)
	}
	if got, _ := StepUnit(held, result(VerdictRed, forTest("TestB"))); got.Phase != PhaseHeld {
		t.Errorf("red of an unrelated test released the hold")
	}
	if got, _ := StepUnit(held, result(VerdictRed, forTest("TestA"))); got.Phase != PhaseOpen || got.Hold != "" {
		t.Errorf("red of the reproducing test: %+v, want open with the hold released", got)
	}
	held.Head = "h1"
	if got, _ := StepUnit(held, ciGreenOf("TestA", "h1")); got.Phase != PhaseHeld {
		t.Errorf("CI green on the held head released the hold")
	}
	if got, _ := StepUnit(held, ciGreenOf("TestA", "h2")); got.Phase != PhaseClosed || got.Hold != "" {
		t.Errorf("CI green of TestA on a later head: %+v, want closed", got)
	}
	merge := ue(KindLaneMerged, func(e *Event) { e.Closes = []string{"esc-other"} })
	if got, _ := StepUnit(held, merge); got.Phase != PhaseHeld {
		t.Errorf("merge closing another escape released the hold")
	}
	merge.Closes = []string{"esc-other", "esc-9"}
	if got, _ := StepUnit(held, merge); got.Phase != PhaseClosed {
		t.Errorf("merge whose closes-by names esc-9: phase %q, want closed", got.Phase)
	}
}

func TestStepUnit_aTestAddedWhileHeldDropsTheHold(t *testing.T) {
	held, _ := StepUnit(unitIn(PhaseClosed), trunkEscape("TestA", "esc-9"))
	got, _ := StepUnit(held, ue(KindEdit, inFile(ClassTest), forTest("TestN"), onTree("t2")))
	if got.Phase != PhasePending || got.Test != "TestN" || got.Hold != "" || !got.Earns {
		t.Fatalf("test added while held: %+v, want pending(TestN), hold dropped, earning a pair", got)
	}
}

func TestStepUnit_aGarbledPhaseMatchesNoRowAndKeepsItsPhase(t *testing.T) {
	got, _ := StepUnit(Unit{Phase: "garbled", Tree: "t1"}, result(VerdictRed, forTest("TestA")))
	if got.Phase != "garbled" {
		t.Fatalf("phase = %q, want it kept", got.Phase)
	}
}

func TestStepUnits_routesByUnitAndNeverWritesIntoItsInput(t *testing.T) {
	in := Units{"pkg/a": unitIn(PhaseClosed), "pkg/b": unitIn(PhaseOpen)}
	before := maps.Clone(in)
	out, _ := StepUnits(in, result(VerdictRed, forTest("TestA")))
	if !reflect.DeepEqual(in, before) {
		t.Fatalf("StepUnits wrote into its input: %+v", in)
	}
	if out["pkg/a"].Phase != PhaseOpen || out["pkg/b"] != in["pkg/b"] {
		t.Fatalf("out = %+v, want only pkg/a to move", out)
	}
	out, _ = StepUnits(in, result(VerdictRed, forTest("TestA"), func(e *Event) { e.Unit = "pkg/new" }))
	if out["pkg/new"].Phase != PhaseOpen || len(out) != 3 {
		t.Fatalf("an event for a new unit: %+v, want it created", out)
	}
}

func TestStepUnits_aGatedCommitWithNoUnitSeedsEveryUnit(t *testing.T) {
	in := Units{"pkg/b": {}, "pkg/a": {}}
	out, _ := StepUnits(in, ue(KindCommitGated, func(e *Event) { e.Unit = "" }, onTree("base")))
	if len(out) != 2 {
		t.Fatalf("out = %+v, want both units", out)
	}
	for name, u := range out {
		if u.LastReal != VerdictGreen || u.LastRealTree != "base" {
			t.Errorf("unit %s: %+v, want last real green @base", name, u)
		}
	}
	out, fx := StepUnits(in, ue(KindEdit, inFile(ClassCode), func(e *Event) { e.Unit = "" }))
	if len(fx) != 0 || !reflect.DeepEqual(out, in) {
		t.Fatalf("a unit-less edit: %+v %v, want nothing", out, fx)
	}
}

// TestUnitTable_everyRowIsReachableAndObeyed walks states against events: a
// winning row must land on its To phase (a dead row fails here), and its guide
// must come out exactly when the row names one.
func TestUnitTable_everyRowIsReachableAndObeyed(t *testing.T) {
	var states []Unit
	for _, p := range []Phase{PhaseClosed, PhasePending, PhaseOpen, PhaseHeld} {
		for _, red := range []string{"", "t0", "t1"} {
			for _, code := range []bool{false, true} {
				for _, earns := range []bool{false, true} {
					u := unitIn(p)
					u.RedTree, u.Code, u.Earns = red, code, earns
					states = append(states, u)
				}
			}
		}
	}
	events := []Event{
		ue(KindEdit, inFile(ClassTest), forTest("TestA")),
		ue(KindEdit, inFile(ClassTest), forTest("TestA"), removed),
		ue(KindEdit, inFile(ClassTest), forTest("TestOther"), removed),
		ue(KindEdit, inFile(ClassCode)),
		ue(KindEdit, inFile(ClassCode), covered),
		ue(KindEdit, inFile(ClassCode), covered, addsSymbol),
		ue(KindEdit, inFile(ClassOther)),
		result(VerdictRed, forTest("TestA")),
		result(VerdictRed, forTest("TestB")),
		result(VerdictRed),
		result(VerdictGreen),
		result(VerdictRedBogus, withCause("x")),
		result(VerdictNotTested, withCause(CauseDeferred)),
		result(VerdictNotTested, withCause(CauseTimeout)),
		ciRed(FailureTest, true, forTest("TestCI")),
		ciRed(FailureTest, false, forTest("TestCI")),
		ciGreenOf("TestA", "h1"), ciGreenOf("TestA", "h0"), ciGreenOf("TestB", "h1"),
		trunkEscape("TestA", "esc-9"),
		ue(KindLaneMerged, func(e *Event) { e.Closes = []string{"esc-1"} }),
		ue(KindLaneMerged, func(e *Event) { e.Closes = []string{"esc-x"} }),
		ue(KindCommitGated),
		ue(KindRunRequested),
	}
	won := map[int]bool{}
	for _, u := range states {
		for _, e := range events {
			next, fx, row := advanceUnit(u, e)
			if row < 0 {
				continue
			}
			won[row] = true
			r := unitTable[row]
			if want := first(string(r.To), string(u.Phase)); string(next.Phase) != want {
				t.Fatalf("row %q: %s + %s landed on %q, want %q", r.Rule, u.Phase, e.Kind, next.Phase, want)
			}
			if r.Guide == "" && len(guides(fx)) != 0 {
				t.Fatalf("row %q named no guide but %v came out", r.Rule, guides(fx))
			}
		}
	}
	for i, r := range unitTable {
		if !won[i] {
			t.Errorf("row %d %q never wins for any state and event: dead or shadowed", i, r.Rule)
		}
	}
}

func TestUnitTable_everyRowNamesItsSourceSection(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range unitTable {
		if !strings.Contains(r.Rule, "§") || seen[r.Rule] {
			t.Errorf("row rule %q does not cite a section or is repeated", r.Rule)
		}
		seen[r.Rule] = true
		if len(r.From) == 0 || r.In == "" {
			t.Errorf("row %q misses a From or In", r.Rule)
		}
	}
}
