package measure

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// abDecisions is what the hook did about one lane's red-green decisions: drops it dropped
// for its budget and blocks it gave.
func abDecisions(arm, lane string, drops, blocks int) []tdd.Event {
	var out []tdd.Event
	for i := range drops {
		out = append(out, shadowAt(0.2+float64(i)/100, lane, "red-green", "unjudged", "cause", "budget", "arm", arm, "arm_why", "assigned"))
	}
	for i := range blocks {
		out = append(out, shadowAt(0.3+float64(i)/100, lane, "red-green", "trellis-stricter", "aphrollo", "block", "arm", arm, "arm_why", "assigned", "lang", "go", "unit", "u"))
	}
	return out
}

// decidedButFor is a log that is decided (warn better) on the escapes alone, with the
// enforce arm's decisions as the test gives them.
func decidedButFor(drops, blocks int) []tdd.Event {
	return abCat(abEscapeLanes("enforce", 12, 12), abEscapeLanes("warn", 12, 0), abDecisions("enforce", "enforce-0", drops, blocks))
}

// A readout never looks decided on dropped data: when more than half of an arm's decisions
// were dropped for the budget, the verdict is deciding, whatever the escapes say.
func TestComputeAB_MostOfAnArmsDecisionsDroppedIsDecidingNotDecided(t *testing.T) {
	ab := computeAB(decidedButFor(3, 1), Options{})
	if ab.Verdict != VerdictDeciding || ab.Decidable {
		t.Errorf("verdict = %q decidable %v with 3 of 4 decisions dropped, want deciding and not decidable", ab.Verdict, ab.Decidable)
	}
	if got := ab.Text(); !strings.Contains(got, "verdict (escapes per lane): deciding (decisions dropped)") {
		t.Errorf("text:\n%s\nwant the verdict line to say decisions dropped", got)
	}
}

// Exactly half is not more than half: the readout stays as decided.
func TestComputeAB_HalfOfAnArmsDecisionsDroppedStaysDecided(t *testing.T) {
	ab := computeAB(decidedButFor(2, 2), Options{})
	if ab.Verdict != VerdictWarnBetter || !ab.Decidable {
		t.Errorf("verdict = %q decidable %v with 2 of 4 decisions dropped, want %q", ab.Verdict, ab.Decidable, VerdictWarnBetter)
	}
	if got := ab.Text(); strings.Contains(got, "decisions dropped)") {
		t.Errorf("text names dropped decisions as the reason at exactly half:\n%s", got)
	}
}

// Each arm says how many of its decisions went unmeasured.
func TestAB_TextSaysHowManyDecisionsEachArmLeftUnmeasured(t *testing.T) {
	got := computeAB(decidedButFor(3, 1), Options{}).Text()
	for _, want := range []string{"3 decisions unmeasured of 4", "0 decisions unmeasured of 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
}

// The share is of one arm's own decisions, not of both arms': a warn arm that dropped
// every one of its few decisions is as undecided as an enforce arm that did.
func TestComputeAB_TheWarnArmDroppingMostDecisionsAlsoMeansDeciding(t *testing.T) {
	events := abCat(abEscapeLanes("enforce", 12, 12), abEscapeLanes("warn", 12, 0),
		abDecisions("enforce", "enforce-0", 0, 20), abDecisions("warn", "warn-0", 3, 1))
	if ab := computeAB(events, Options{}); ab.Verdict != VerdictDeciding {
		t.Errorf("verdict = %q, want deciding: the warn arm dropped 3 of 4", ab.Verdict)
	}
}

// A deny the holdout arm turned into a guide is still a decision the hook answered: it
// counts among the arm's decisions, so two dropped beside two held out is half, not all.
func TestComputeAB_HeldOutDecisionsCountAmongTheDecisionsAnArmAnswered(t *testing.T) {
	var held []tdd.Event
	for i := range 2 {
		held = append(held, shadowAt(0.4+float64(i)/100, "enforce-0", "red-green", "trellis-stricter", "aphrollo", "warn", "held_out", "true", "arm", "enforce", "arm_why", "assigned", "unit", "u"))
	}
	events := abCat(abEscapeLanes("enforce", 12, 12), abEscapeLanes("warn", 12, 0), abDecisions("enforce", "enforce-0", 2, 0), held)
	if ab := computeAB(events, Options{}); ab.Verdict != VerdictWarnBetter {
		t.Errorf("verdict = %q, want %q: 2 of 4 decisions dropped is not more than half", ab.Verdict, VerdictWarnBetter)
	}
}
