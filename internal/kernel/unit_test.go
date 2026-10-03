package kernel

import (
	"reflect"
	"slices"
	"testing"
)

// ue builds a TDD-machine event on lane "fix", unit "pkg/a", at tree t1.
func ue(kind Kind, opts ...func(*Event)) Event {
	e := Event{Kind: kind, Lane: "fix", Actor: "s/a", At: t0, Unit: "pkg/a", Tree: "t1"}
	for _, o := range opts {
		o(&e)
	}
	return e
}

func onTree(tr string) func(*Event)   { return func(e *Event) { e.Tree = tr } }
func inFile(c FileClass) func(*Event) { return func(e *Event) { e.File = c } }
func forTest(n string) func(*Event)   { return func(e *Event) { e.Test = n } }
func withCause(c string) func(*Event) { return func(e *Event) { e.Cause = c } }
func inMode(m TDDMode) func(*Event)   { return func(e *Event) { e.Mode = m } }
func onJob(j string) func(*Event)     { return func(e *Event) { e.Job = j } }
func covered(e *Event)                { e.Covered = true }
func addsSymbol(e *Event)             { e.AddsSymbol = true }
func removed(e *Event)                { e.Removed = true }

func result(v Verdict, opts ...func(*Event)) Event {
	return ue(KindRunResult, append([]func(*Event){func(e *Event) { e.Verdict = v }}, opts...)...)
}

func ciRed(failure string, gated bool, opts ...func(*Event)) Event {
	return ue(KindCIVerdict, append([]func(*Event){func(e *Event) {
		e.Conclusion, e.Failure, e.Gated, e.Tree = CIRed, failure, gated, ""
	}}, opts...)...)
}

func ciGreenOf(test, h string) Event {
	return ue(KindCIVerdict, forTest(test), func(e *Event) { e.Conclusion, e.Head, e.Tree = CIGreen, h, "" })
}

func trunkEscape(test, id string) Event {
	return ue(KindEscape, forTest(test), func(e *Event) { e.Stage, e.EscapeID, e.Tree = StageTrunk, id, "" })
}

func guides(fx []Effect) []string {
	var out []string
	for _, f := range fx {
		if f.Kind == EffectGuide {
			out = append(out, f.Detail)
		}
	}
	return out
}

func hasKind(fx []Effect, k EffectKind) bool {
	return slices.ContainsFunc(fx, func(f Effect) bool { return f.Kind == k })
}

func unitIn(p Phase) Unit {
	switch p {
	case PhasePending:
		return Unit{Phase: PhasePending, Test: "TestA", Earns: true, Tree: "t1"}
	case PhaseOpen:
		return Unit{Phase: PhaseOpen, Test: "TestA", Earns: true, Tree: "t1", RedTree: "t0"}
	case PhaseHeld:
		return Unit{Phase: PhaseHeld, Test: "TestA", Hold: "esc-1", Head: "h0", Tree: "t1"}
	}
	return Unit{Phase: PhaseClosed, Tree: "t1"}
}

// TestStepUnit_everyCellOfTheDocumentTable holds the §3 "TDD machine" table
// cell by cell: four states against the six events, plus the escape split.
// The expectations are the document's words, not read back from the code.
func TestStepUnit_everyCellOfTheDocumentTable(t *testing.T) {
	testEdit := ue(KindEdit, inFile(ClassTest), forTest("TestA"), onTree("t2"))
	red := result(VerdictRed, forTest("TestA"))
	codeEdit := ue(KindEdit, inFile(ClassCode), onTree("t2"))
	green := result(VerdictGreen)
	bogus := result(VerdictRedBogus, withCause("build failed"))
	ciEscape := ciRed(FailureTest, true, forTest("TestCI"), func(e *Event) { e.Head = "h1" })
	trunk := trunkEscape("TestA", "esc-9")

	type cell struct {
		name  string
		e     Event
		want  Phase
		guide []string
	}
	grid := map[Phase][]cell{
		PhaseClosed: {
			{"test added", testEdit, PhasePending, nil},
			{"real red", red, PhaseOpen, nil},
			{"code edit, not tested code", codeEdit, PhaseClosed, []string{GuideUntestedCode}},
			{"green", green, PhaseClosed, nil},
			{"bogus red", bogus, PhaseClosed, []string{GuideRedBogus}},
			{"CI test red of a gated head", ciEscape, PhaseOpen, nil},
			{"trunk escape", trunk, PhaseHeld, nil},
		},
		PhasePending: {
			{"test edited again", testEdit, PhasePending, nil},
			{"real red", red, PhaseOpen, nil},
			{"code edit allowed while the verdict is on its way", codeEdit, PhasePending, nil},
			{"green at once", green, PhaseClosed, []string{GuidePassedAtOnce}},
			{"bogus red", bogus, PhasePending, []string{GuideRedBogus}},
			{"CI test red of a gated head", ciEscape, PhaseOpen, nil},
			{"trunk escape", trunk, PhaseHeld, nil},
		},
		PhaseOpen: {
			{"test edited stays open", testEdit, PhaseOpen, nil},
			{"red again", red, PhaseOpen, nil},
			{"code edit allowed", codeEdit, PhaseOpen, nil},
			{"green", green, PhaseClosed, nil},
			{"bogus red: an open red survives a build break", bogus, PhaseOpen, []string{GuideRedBogus}},
			{"CI escape while open", ciEscape, PhaseOpen, nil},
			{"trunk escape while open", trunk, PhaseOpen, nil},
		},
		PhaseHeld: {
			{"test added", testEdit, PhasePending, nil},
			{"local red of the reproducing test", red, PhaseOpen, nil},
			{"code edit gets guidance", codeEdit, PhaseHeld, []string{GuideHeld}},
			{"a passing test does not reproduce the escape", green, PhaseHeld, nil},
			{"bogus red", bogus, PhaseHeld, []string{GuideRedBogus}},
			{"CI escape while held", ciEscape, PhaseHeld, nil},
			{"trunk escape while held", trunk, PhaseHeld, nil},
		},
	}
	for from, cells := range grid {
		for _, c := range cells {
			got, fx := StepUnit(unitIn(from), c.e)
			if got.Phase != c.want || !slices.Equal(guides(fx), c.guide) {
				t.Errorf("%s + %s: phase %q guides %v, want %q and %v", from, c.name, got.Phase, guides(fx), c.want, c.guide)
			}
		}
	}
}

func TestStepUnit_aRedThenGreenOnALaterTreeRecordsThePair(t *testing.T) {
	u := Unit{}
	steps := []struct {
		e     Event
		phase Phase
	}{
		{ue(KindEdit, inFile(ClassTest), forTest("TestA"), onTree("t2")), PhasePending},
		{ue(KindRunRequested, onTree("t2")), PhasePending},
		{result(VerdictRed, forTest("TestA"), onTree("t2")), PhaseOpen},
		{ue(KindEdit, inFile(ClassCode), onTree("t3")), PhaseOpen},
		{result(VerdictGreen, onTree("t3")), PhaseClosed},
	}
	for i, st := range steps {
		u, _ = StepUnit(u, st.e)
		if u.Phase != st.phase {
			t.Fatalf("step %d (%s): phase %q, want %q", i, st.e.Kind, u.Phase, st.phase)
		}
	}
	if want := (Pair{Test: "TestA", Red: "t2", Green: "t3"}); u.Pair != want {
		t.Fatalf("pair = %+v, want %+v", u.Pair, want)
	}
}

func TestStepUnit_aTestThatPassesAtOnceIsNotARed(t *testing.T) {
	u, _ := StepUnit(unitIn(PhasePending), result(VerdictGreen))
	if u.Phase != PhaseClosed || u.Pair != (Pair{}) {
		t.Fatalf("pending + green: phase %q pair %+v, want closed and no pair", u.Phase, u.Pair)
	}
}

func TestStepUnit_aRedOfAnExistingTestIsAnOpenRedThatEarnsNoPair(t *testing.T) {
	u, _ := StepUnit(Unit{Phase: PhaseClosed, Tree: "t1"}, result(VerdictRed, forTest("TestOld")))
	if u.Phase != PhaseOpen || u.Test != "TestOld" || u.Earns {
		t.Fatalf("closed + red of TestOld: %+v, want open(TestOld) that earns no pair", u)
	}
	u, _ = StepUnit(u, ue(KindEdit, inFile(ClassCode), onTree("t2")))
	u, _ = StepUnit(u, result(VerdictGreen, onTree("t2")))
	if u.Phase != PhaseClosed || u.Pair != (Pair{}) {
		t.Fatalf("after the fix: phase %q pair %+v, want closed and no pair: only a new or changed test earns one", u.Phase, u.Pair)
	}
}

func TestStepUnit_aResultForAnOlderTreeChangesNothingAndIsLabelledStale(t *testing.T) {
	u := unitIn(PhaseOpen)
	u.Tree = "t3"
	got, fx := StepUnit(u, result(VerdictGreen, onTree("t2")))
	if !reflect.DeepEqual(got, u) {
		t.Fatalf("stale green moved the state: %+v -> %+v", u, got)
	}
	if !slices.Equal(guides(fx), []string{GuideStale}) {
		t.Fatalf("stale result guides = %v, want [%s]: it is delivered, labelled stale", guides(fx), GuideStale)
	}
}

func TestStepUnit_aResultWithNoTreeIsStaleOnceTheUnitKnowsItsTree(t *testing.T) {
	u := unitIn(PhaseOpen)
	got, _ := StepUnit(u, result(VerdictGreen, onTree("")))
	if !reflect.DeepEqual(got, u) {
		t.Fatalf("a green with no tree closed the unit: %+v", got)
	}
}

func TestStepUnit_aUnitWithNoTreeAdoptsTheFirstResultsTree(t *testing.T) {
	u, _ := StepUnit(Unit{}, result(VerdictRed, forTest("TestA"), onTree("t7")))
	if u.Tree != "t7" || u.Phase != PhaseOpen {
		t.Fatalf("unit = %+v, want it open on tree t7", u)
	}
}

func TestStepUnit_notTestedResultsNeverMoveAPhaseOrCountAsGreen(t *testing.T) {
	causes := []string{CauseTimeout, CauseSkipped, CauseQueuedSkipped, CauseDeferred, CauseInfra, ""}
	for _, p := range []Phase{PhaseClosed, PhasePending, PhaseOpen, PhaseHeld} {
		for _, c := range causes {
			for _, v := range []Verdict{VerdictNotTested, "garbage", ""} {
				u := unitIn(p)
				u.Unproven = true
				got, fx := StepUnit(u, result(v, withCause(c)))
				if got.Phase != p || got.LastReal != "" || got.Pair != (Pair{}) || !got.Unproven {
					t.Errorf("%s + %q/%q: %+v, want phase, last real, pair and unproven untouched", p, v, c, got)
				}
				if len(guides(fx)) != 1 {
					t.Errorf("%s + %q/%q: guides %v, want the cause named once", p, v, c, guides(fx))
				}
			}
		}
	}
}

func TestStepUnit_aDeferredRunIsPendingNotNotTested(t *testing.T) {
	_, fx := StepUnit(unitIn(PhaseOpen), result(VerdictNotTested, withCause(CauseDeferred)))
	if got := guides(fx); !slices.Equal(got, []string{GuidePending}) {
		t.Fatalf("deferred guides = %v, want [%s]", got, GuidePending)
	}
	_, fx = StepUnit(unitIn(PhaseOpen), result(VerdictNotTested, withCause(CauseTimeout)))
	if got := guides(fx); !slices.Equal(got, []string{GuideNotTested}) {
		t.Fatalf("timeout guides = %v, want [%s]", got, GuideNotTested)
	}
}

func TestStepUnit_theNamedCauseTravelsWithTheGuidance(t *testing.T) {
	_, fx := StepUnit(unitIn(PhaseClosed), result(VerdictNotTested, withCause(CauseInfra), forTest("TestA")))
	if len(fx) != 1 || fx[0].Cause != CauseInfra || fx[0].Unit != "pkg/a" || fx[0].Lane != "fix" {
		t.Fatalf("effects = %+v, want one guide naming cause %q on unit pkg/a, lane fix", fx, CauseInfra)
	}
}

func TestStepUnit_aGreenAndARedAtTheSameTreeIsAFlakeNotAClose(t *testing.T) {
	u, _ := StepUnit(unitIn(PhaseClosed), result(VerdictRed, forTest("TestA")))
	got, fx := StepUnit(u, result(VerdictGreen))
	if got.Phase != PhaseOpen || !slices.Equal(guides(fx), []string{GuideFlaky}) {
		t.Fatalf("green at the red's own tree: phase %q guides %v, want open and [%s]", got.Phase, guides(fx), GuideFlaky)
	}
}

func TestStepUnit_aTestEditedWhileOpenClosesOnlyIfItThenPassesWithNoCodeChange(t *testing.T) {
	u := unitIn(PhaseOpen)
	u, _ = StepUnit(u, ue(KindEdit, inFile(ClassTest), forTest("TestA"), onTree("t2")))
	if u.Phase != PhaseOpen {
		t.Fatalf("test edit while open: phase %q, want open", u.Phase)
	}
	u, _ = StepUnit(u, result(VerdictGreen, onTree("t2")))
	if u.Phase != PhaseClosed || u.Pair != (Pair{}) {
		t.Fatalf("green after a test-only change: phase %q pair %+v, want closed with no pair", u.Phase, u.Pair)
	}
}

func TestStepUnit_aNewRedAfterACodeEditMovesTheRedForward(t *testing.T) {
	u := unitIn(PhaseOpen)
	u.RedTree = "t1"
	u, _ = StepUnit(u, ue(KindEdit, inFile(ClassCode), onTree("t2")))
	u, _ = StepUnit(u, result(VerdictRed, forTest("TestA"), onTree("t2")))
	u, _ = StepUnit(u, ue(KindEdit, inFile(ClassCode), onTree("t3")))
	u, _ = StepUnit(u, result(VerdictGreen, onTree("t3")))
	if want := (Pair{Test: "TestA", Red: "t2", Green: "t3"}); u.Pair != want {
		t.Fatalf("pair = %+v, want %+v: the red nearest the green", u.Pair, want)
	}
}

func TestStepUnit_removingTheTestClosesAnOpenOrPendingUnit(t *testing.T) {
	for _, p := range []Phase{PhaseOpen, PhasePending} {
		got, _ := StepUnit(unitIn(p), ue(KindEdit, inFile(ClassTest), forTest("TestA"), removed, onTree("t2")))
		if got.Phase != PhaseClosed || got.Test != "" {
			t.Errorf("%s + T removed: %+v, want closed with no T", p, got)
		}
	}
	got, _ := StepUnit(unitIn(PhaseOpen), ue(KindEdit, inFile(ClassTest), forTest("TestOther"), removed, onTree("t2")))
	if got.Phase != PhaseOpen {
		t.Errorf("another test removed: phase %q, want open", got.Phase)
	}
}
