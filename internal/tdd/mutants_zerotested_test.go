package tdd

import (
	"strings"
	"testing"
	"time"
)

// A diff the tool can produce no mutants for is legitimate — some changes
// are not mutatable — so this is not a refusal. What it must not be is
// INDISTINGUISHABLE from a run that caught a hundred: today both end in
// "mutants-passed:", and a reader (and `gate stats`) cannot tell a
// measurement from an empty pool. The stand-down has to name itself.
func TestJudgeMutants_AZeroTestedRunNamesItselfInsteadOfPassing(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{}, nil)

	if v.Refused {
		t.Fatalf("an unmutatable diff is not a refusal: %+v", v)
	}
	if v.Skipped == "" {
		t.Fatalf("a run that tested 0 mutants must record itself as a stand-down, not a pass: %+v", v)
	}
	if !strings.Contains(v.Message, "0 mutants") && !strings.Contains(v.Message, "no mutants") {
		t.Fatalf("the report must say in words that nothing was measured; got:\n%s", v.Message)
	}
}

// The gate log is the other half: `gate stats` classifies any
// `mutants-passed:` line as a green measurement, so a zero-tested run logged
// under that prefix inflates the very number that is supposed to say how
// often the lane's tests were actually proven against mutants.
func TestMeasureLogVerdict_ZeroTestedIsNotLoggedAsAGreenMeasurement(t *testing.T) {
	t.Parallel()
	line := measureLogVerdict(judgeMutants(MutantsConfig{}, nil))
	if strings.HasPrefix(line, "mutants-passed:") {
		t.Fatalf("a run that tested nothing must not be logged as a passed measurement; got %q", line)
	}

	outcome, reason, ok := mutantsOutcome(line)
	if !ok {
		t.Fatalf("gate stats does not recognise %q at all", line)
	}
	if outcome == "green" {
		t.Fatalf("gate stats counts %q as a green measurement", line)
	}
	if reason == "" {
		t.Fatalf("a stand-down must be counted under a reason of its own; got %q from %q", reason, line)
	}
}

// End to end through the stats reader, in the shape the table is actually
// read: a zero-tested run adds nothing to the green column and shows up as
// its own reason.
func TestStats_ZeroTestedMutationRunIsNotCountedGreen(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	log := stamp(at, "mutants", "/repo", "mutants", measureLogVerdict(judgeMutants(MutantsConfig{}, nil)), 0)

	s := GateStats(strings.NewReader(log), time.Time{})
	if got := s.Count("mutants", "green"); got != 0 {
		t.Fatalf("mutants green = %d, want 0 — nothing was measured", got)
	}
	if len(s.Mutants) == 0 {
		t.Fatalf("the run is counted nowhere at all: %v", s.Mutants)
	}
}

// A run that DID measure something keeps passing exactly as before: the
// stand-down is scoped to an empty pool, not to any quiet run.
func TestJudgeMutants_AMeasuredRunStillPasses(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{}, []MutantOutcome{{Status: "caught", File: "a.rs", Line: 1}})

	if v.Skipped != "" {
		t.Fatalf("a run that tested a mutant is a measurement, not a stand-down: %+v", v)
	}
	if v.Refused {
		t.Fatalf("a caught mutant is a pass: %+v", v)
	}
	if got := measureLogVerdict(v); !strings.HasPrefix(got, "mutants-passed:") {
		t.Fatalf("measureLogVerdict = %q, want the passed measurement form", got)
	}
}
