package kernel

import (
	"slices"
	"testing"
)

func TestAdvanceUnit_reportsNoRowWhenTheEventIsFrozenDroppedStaleOrIgnored(t *testing.T) {
	u := unitIn(PhaseOpen)
	u.LastJob = "j1"
	cases := []struct {
		name string
		e    Event
	}{
		{"frozen by off", result(VerdictGreen, inMode(ModeOff))},
		{"retried job", result(VerdictGreen, onJob("j1"))},
		{"stale tree", result(VerdictGreen, onTree("t9"))},
		{"no row for the input", ue(KindEdit, inFile(ClassOther))},
	}
	for _, c := range cases {
		if _, _, row := advanceUnit(u, c.e); row != -1 {
			t.Errorf("%s: row = %d, want -1", c.name, row)
		}
	}
}

func TestStepUnit_aStaleGuideCarriesTheTreeItWasAbout(t *testing.T) {
	_, fx := StepUnit(unitIn(PhaseOpen), result(VerdictGreen, onTree("t9")))
	if len(fx) != 1 || fx[0].Tree != "t9" || fx[0].Unit != "pkg/a" {
		t.Fatalf("effects = %+v, want one guide about tree t9 on pkg/a", fx)
	}
}

func TestStepUnit_aGuideCarriesTheUnitsTest(t *testing.T) {
	_, fx := StepUnit(unitIn(PhaseHeld), ue(KindEdit, inFile(ClassCode), onTree("t2")))
	i := slices.IndexFunc(fx, func(f Effect) bool { return f.Kind == EffectGuide })
	if i < 0 || fx[i].Test != "TestA" {
		t.Fatalf("effects = %+v, want the held guide to name TestA", fx)
	}
}

func TestStepUnit_aRedAtANewTreeStartsANewCountOfCodeEditsButARedAtTheSameTreeKeepsIt(t *testing.T) {
	u := unitIn(PhaseOpen)
	u.RedTree, u.Code = "t0", true
	got, _ := StepUnit(u, result(VerdictRed, forTest("TestA")))
	if got.RedTree != "t1" || got.Code {
		t.Fatalf("red at a new tree: red %q code %v, want t1 and no code edit counted", got.RedTree, got.Code)
	}
	u.RedTree = "t1"
	got, _ = StepUnit(u, result(VerdictRed, forTest("TestA")))
	if got.RedTree != "t1" || !got.Code {
		t.Fatalf("red again at the same tree: red %q code %v, want t1 and the code edit kept", got.RedTree, got.Code)
	}
}

func TestStepUnit_aRedNamingNothingIsTakenAsTheUnitsOwn(t *testing.T) {
	if got, _ := StepUnit(unitIn(PhasePending), result(VerdictRed)); got.Phase != PhaseOpen || got.Test != "TestA" || !got.Earns {
		t.Errorf("pending + unnamed red: %+v, want open(TestA) still earning a pair", got)
	}
	held, _ := StepUnit(unitIn(PhaseClosed), trunkEscape("", "esc-9"))
	if got, _ := StepUnit(held, result(VerdictRed, forTest("TestB"))); got.Phase != PhaseOpen || got.Test != "TestB" {
		t.Errorf("held with no named test + red of TestB: %+v, want open(TestB)", got)
	}
}

func TestStepUnit_aTestRemovalNamingNothingClosesTheUnit(t *testing.T) {
	got, _ := StepUnit(unitIn(PhaseOpen), ue(KindEdit, inFile(ClassTest), removed, onTree("t2")))
	if got.Phase != PhaseClosed {
		t.Fatalf("unnamed removal: phase %q, want closed", got.Phase)
	}
}

func TestStepUnit_aCIGreenCloseNeedsANamedTestButNotARecordedHead(t *testing.T) {
	u := unitIn(PhaseOpen) // opened locally: it records no head
	if got, _ := StepUnit(u, ciGreenOf("TestA", "h7")); got.Phase != PhaseClosed {
		t.Errorf("CI green of TestA on a unit with no recorded head: phase %q, want closed", got.Phase)
	}
	if got, _ := StepUnit(u, ciGreenOf("", "h7")); got.Phase != PhaseOpen {
		t.Errorf("CI green naming no test: phase %q, want open", got.Phase)
	}
	u.Test = ""
	if got, _ := StepUnit(u, ciGreenOf("", "h7")); got.Phase != PhaseOpen {
		t.Errorf("CI green naming no test of a unit with no T: phase %q, want open", got.Phase)
	}
}

func TestStepUnits_aNilMapIsAnEmptyOne(t *testing.T) {
	out, fx := StepUnits(nil, ue(KindEdit, inFile(ClassOther), func(e *Event) { e.Unit = "" }))
	if out == nil || len(out) != 0 || len(fx) != 0 {
		t.Fatalf("StepUnits(nil) = %+v %v, want an empty non-nil map", out, fx)
	}
}

func TestStepUnit_aMergeWithNoClosesNamesNoEscape(t *testing.T) {
	held := unitIn(PhaseHeld)
	held.Hold = ""
	if got, _ := StepUnit(held, ue(KindLaneMerged)); got.Phase != PhaseHeld {
		t.Fatalf("plain merge: phase %q, want held", got.Phase)
	}
	if got, _ := StepUnit(held, ue(KindLaneMerged, func(e *Event) { e.Closes = []string{""} })); got.Phase != PhaseHeld {
		t.Fatalf("merge closing an empty id released a hold with no id: phase %q", got.Phase)
	}
}
