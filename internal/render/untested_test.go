package render

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func untestedDecision(o kernel.Outcome) kernel.Decision {
	return kernel.Decision{Outcome: o, Rule: "red-green",
		Cause: "a code edit that is not tested code, with no open red", Next: "write the failing test first, then edit the code"}
}

// A guide names the unit and the next step, in at most the guidance cap.
func TestUntested_AGuideNamesTheUnitAndTheNextStepInTheGuidanceCap(t *testing.T) {
	l := Untested(untestedDecision(kernel.OutcomeGuide), "internal/lane", "never shown on a guide")
	want := "red-green (internal/lane): a code edit that is not tested code, with no open red · do: write the failing test first, then edit the code"
	if l.Text != want || l.Kind != KindGuide {
		t.Errorf("guide = %q (%s), want %q (guide)", l.Text, l.Kind, want)
	}
	checkLine(t, l)
}

// A deny names the override it offers, and stays inside the deny cap.
func TestUntested_ADenyNamesItsOverrideInTheDenyCap(t *testing.T) {
	override := "aphrollo gate allow red-green (this session), or aphrollo config set tdd warn"
	l := Untested(untestedDecision(kernel.OutcomeDeny), "internal/lane", override)
	want := "red-green blocked (internal/lane): a code edit that is not tested code, with no open red · do: write the failing test first, then edit the code · override: " + override
	if l.Text != want || l.Kind != KindDeny {
		t.Errorf("deny = %q (%s), want %q (deny)", l.Text, l.Kind, want)
	}
	checkLine(t, l)
}

// A unit with a long path is cut and named, never allowed to push a line over its cap.
func TestUntested_ALongUnitIsCutAndTheLineStaysInItsCap(t *testing.T) {
	unit := "typescript:" + strings.Repeat("packages/some-long-directory-name/", 12)
	for _, o := range []kernel.Outcome{kernel.OutcomeGuide, kernel.OutcomeDeny} {
		l := Untested(untestedDecision(o), unit, strings.Repeat("o", 400))
		checkLine(t, l)
		if !strings.Contains(l.Text, "cut:") {
			t.Errorf("%s: a %d-byte unit and a 400-byte override were not cut: %q", o, len(unit), l.Text)
		}
	}
}
