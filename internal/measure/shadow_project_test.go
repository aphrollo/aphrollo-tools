package measure

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// pjFire is a would-be block of red-green on a project-root unit (Python, TypeScript):
// the fire names the project's root, for no package pattern can say what a proof of
// it ran.
func pjFire(sec float64, lane, unit, root string) tdd.Event {
	return shadowAt(sec, lane, "red-green", "trellis-stricter", "live_rule", "commit-proof", "unit", unit, "unit_root", root)
}

// pjProofIn is the commit gate's proof stage on lane, run in the project at root.
func pjProofIn(sec float64, lane, verdict, cmd, root string) tdd.Event {
	return ev(sec, lane, "commit_gate", func(e *tdd.Event) { e.Stage, e.Verdict, e.Cmd, e.Root = "precommit", verdict, cmd, root })
}

func TestShadow_APythonOrTypeScriptWouldBeBlockIsJudgedByTheCommitProofRunInItsProject(t *testing.T) {
	events := []tdd.Event{
		// a refused proof run in the unit's project is the catch
		pjFire(0, "catch", "python:backend", "/w/backend"), pjProofIn(600, "catch", "violated", "python -m pytest tests", "/w/backend"),
		// a proof that passed there is the pass
		pjFire(0, "pass", "typescript:frontend", "/w/frontend"), pjProofIn(600, "pass", "red-proven", "npx vitest run", "/w/frontend"),
		// a proof of another project says nothing of this unit
		pjFire(0, "other", "python:backend", "/w/backend"), pjProofIn(600, "other", "violated", "npx vitest run", "/w/frontend"),
		// the same path spelled with backslashes and a trailing separator is the same project
		pjFire(0, "spelled", "python:backend", "/w/backend"), pjProofIn(600, "spelled", "violated", "pytest", `\w\backend\`),
		// a fire of a Go unit is still judged by packages, not by where the proof ran
		redGreenFire(0, "go", "internal/lane"), pjProofIn(600, "go", "violated", "go test ./internal/store", "/w"),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "red-green")
	want := ShadowRule{Rule: "red-green", Fires: 5, Stricter: 5, Catches: 2, Passes: 1, Open: 2}
	if got != want {
		t.Errorf("red-green = %+v, want %+v", got, want)
	}
}

// The share of records the hook's budget dropped is printed with its count, over
// every rule: a dropped record is an unjudged one naming the budget.
func TestShadow_TheBudgetDroppedShareIsCountedAndPrintedOverEveryRule(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "red-green", "trellis-stricter"),
		shadowAt(1, "a", "red-green", "unjudged", "cause", "budget"),
		shadowAt(2, "a", "facts", "unjudged", "cause", "budget"),
		shadowAt(3, "a", "run-verdict", "agree"),
		shadowAt(4, "a", "run-verdict", "unjudged", "cause", "no-tree"),
	}
	s := computeShadow(events, Options{})
	if s.Fires != 5 || s.Dropped != 2 {
		t.Errorf("fires %d dropped %d, want 5 and 2", s.Fires, s.Dropped)
	}
	if want := "budget drops          2 of 5 records\n"; !strings.Contains(s.Text(), want) {
		t.Errorf("text lacks %q:\n%s", want, s.Text())
	}
}

func TestShadow_TheBudgetDroppedSharePrintsItsPercentOnceThereAreTenRecords(t *testing.T) {
	var events []tdd.Event
	for i := range 10 {
		if i == 0 {
			events = append(events, shadowAt(float64(i), "a", "red-green", "unjudged", "cause", "budget"))
			continue
		}
		events = append(events, shadowAt(float64(i), "a", "run-verdict", "agree"))
	}
	if want := "budget drops          1 of 10 records (10.0%)\n"; !strings.Contains(computeShadow(events, Options{}).Text(), want) {
		t.Errorf("text lacks %q", want)
	}
}

func TestShadow_TheDroppedLinePrintsAtTenRecordsEvenWithNoDrop(t *testing.T) {
	var events []tdd.Event
	for i := range 10 {
		events = append(events, shadowAt(float64(i), "a", "run-verdict", "agree"))
	}
	if want := "budget drops          0 of 10 records (0.0%)\n"; !strings.Contains(computeShadow(events, Options{}).Text(), want) {
		t.Errorf("text lacks %q", want)
	}
	if strings.Contains(computeShadow(events[:9], Options{}).Text(), "budget drops") {
		t.Error("nine records with no drop printed a drop line")
	}
}

func pjLangRow(t *testing.T, s Shadow, lang string) ShadowLang {
	t.Helper()
	for _, l := range s.Languages {
		if l.Lang == lang {
			return l
		}
	}
	t.Fatalf("no row for language %q in %+v", lang, s.Languages)
	return ShadowLang{}
}

func TestShadow_AgreementIsCountedPerLanguageAndPerArm(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "red-green", "agree", "lang", "go"),
		shadowAt(1, "a", "red-green", "trellis-stricter", "lang", "go", "held_out", "true"),
		shadowAt(2, "a", "run-verdict", "agree", "lang", "python"),
		shadowAt(3, "a", "run-verdict", "verdict-mismatch", "lang", "python"),
		shadowAt(4, "a", "red-green", "unjudged", "lang", "python", "cause", "budget"),
		shadowAt(5, "a", "red-green", "trellis-softer", "lang", "ts", "held_out", "true"),
		shadowAt(6, "a", "stop-red", "agree"), // no language: counted in its rule, in no language row
	}
	s := computeShadow(events, Options{})
	if got, want := pjLangRow(t, s, "go"), (ShadowLang{Lang: "go", Fires: 2, Agree: 1, Stricter: 1, HeldOut: 1}); got != want {
		t.Errorf("go = %+v, want %+v", got, want)
	}
	if got, want := pjLangRow(t, s, "python"), (ShadowLang{Lang: "python", Fires: 3, Agree: 1, Mismatch: 1, Unjudged: 1, Dropped: 1}); got != want {
		t.Errorf("python = %+v, want %+v", got, want)
	}
	if got, want := pjLangRow(t, s, "ts"), (ShadowLang{Lang: "ts", Fires: 1, Softer: 1, HeldOut: 1}); got != want {
		t.Errorf("ts = %+v, want %+v", got, want)
	}
	if len(s.Languages) != 3 || s.Languages[0].Lang != "go" || s.Languages[1].Lang != "python" || s.Languages[2].Lang != "ts" {
		t.Errorf("languages = %+v, want go, python, ts in that order", s.Languages)
	}
	text := s.Text()
	for _, want := range []string{
		"language go            2 fires  agree 1  would-be block 1  softer 0  mismatch 0  not comparable 0  unjudged 0 (0 budget)  held out 1  under 10 fires: no rate\n",
		"language python        3 fires  agree 1  would-be block 0  softer 0  mismatch 1  not comparable 0  unjudged 1 (1 budget)  held out 0  under 10 fires: no rate\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestShadow_ALanguageRowPrintsItsAgreementFromTenFires(t *testing.T) {
	var events []tdd.Event
	for i := range 10 {
		rel := "agree"
		if i >= 7 {
			rel = "trellis-stricter"
		}
		events = append(events, shadowAt(float64(i), "a", "red-green", rel, "lang", "python"))
	}
	want := "language python       10 fires  agree 7  would-be block 3  softer 0  mismatch 0  not comparable 0  unjudged 0 (0 budget)  held out 0  agreement 70%\n"
	if text := computeShadow(events, Options{}).Text(); !strings.Contains(text, want) {
		t.Errorf("text lacks %q:\n%s", want, text)
	}
}

// A root can hold a Go module and a Python project: a Go proof refused there is no
// catch of a Python fire, and the ledger's label is a proof of no language.
func TestShadow_AProofCountsForAFireOnlyWhenItsRunnerIsOfTheFiresLanguage(t *testing.T) {
	events := []tdd.Event{
		pjFire(0, "goproof", "python:.", "/w"), pjProofIn(600, "goproof", "violated", "go test ./...", "/w"),
		pjFire(0, "label", "python:.", "/w"), pjProofIn(600, "label", "red-proven", "postedit-ledger", "/w"),
		pjFire(0, "tsproof", "python:.", "/w"), pjProofIn(600, "tsproof", "red-proven", "npx vitest run", "/w"),
		pjFire(0, "right", "python:.", "/w"), pjProofIn(600, "right", "violated", "python -m pytest", "/w"),
		pjFire(0, "rightts", "typescript:.", "/w"), pjProofIn(600, "rightts", "red-proven", "npx vitest run", "/w"),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "red-green")
	want := ShadowRule{Rule: "red-green", Fires: 5, Stricter: 5, Catches: 1, Passes: 1, Open: 3}
	if got != want {
		t.Errorf("red-green = %+v, want %+v", got, want)
	}
}
