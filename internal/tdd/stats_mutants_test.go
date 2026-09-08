package tdd

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// The mutation stage writes one gate-log line per verdict, and `gate stats`
// is where "how many merges did it refuse, and for what" gets answered
// without re-running anything. The stage this replaces refused 150 merges
// over three weeks and logged no reason for 141 of them; a refusal whose
// cause is not counted is exactly that failure again.
//
// A run that REACHED a verdict lands in the green/red columns. One that never
// measured anything — no disk, a tree the run left changed, an exit that
// reached no verdict — has no measurement to report, so it is counted by its
// reason instead of scoring a red that would read as "a mutant survived".
func TestStats_MutantsStageRowCountsRefusalsByReason(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	log := strings.Join([]string{
		stamp(at, "mutants", "/repo", "mutants", "mutants-passed:tested=4,caught=4,unviable=0,missed=0,accepted=0,unmeasured=0,notcovered=0", 0),
		stamp(at, "mutants", "/repo", "mutants", "mutants-passed:tested=9,caught=8,unviable=1,missed=0,accepted=0,unmeasured=0,notcovered=0", 0),
		stamp(at, "mutants", "/repo", "mutants", "mutants-passed:tested=2,caught=1,unviable=0,missed=1,accepted=1,unmeasured=0,notcovered=0", 0),
		stamp(at, "mutants", "/repo", "mutants", "mutants-refused:tested=6,caught=4,unviable=0,missed=2,accepted=0,unmeasured=0,notcovered=0", 0),
		stamp(at, "mutants", "/repo", "mutants", "mutants-refused:disk", 0),
		stamp(at, "mutants", "/repo", "mutants", "mutants-refused:disk", 0),
	}, "")

	s := GateStats(strings.NewReader(log), time.Time{})

	if got := s.Count("mutants", "green"); got != 3 {
		t.Errorf("mutants green = %d, want 3 — one per run that reached a verdict and passed", got)
	}
	if got := s.Count("mutants", "red"); got != 1 {
		t.Errorf("mutants red = %d, want 1 — only the refusal that measured something", got)
	}
	if s.Mutants["disk"] != 2 {
		t.Errorf("mutation stage reasons = %v, want disk=2", s.Mutants)
	}
	if s.Mutants["survivor"] != 1 {
		t.Errorf("mutation stage reasons = %v, want the counted refusal under survivor=1", s.Mutants)
	}

	out := RenderGateStats(s)
	if !regexp.MustCompile(`(?m)^mutants\s+3\s+1\s`).MatchString(out) {
		t.Errorf("no mutants row reading 3 green, 1 red in:\n%s", out)
	}
	if !strings.Contains(out, "disk=2") {
		t.Errorf("rendered stats never say what the refusals were:\n%s", out)
	}
}

// A stand-down is not a refusal and must not be filed as one: the Go runner
// on Windows reports 0.00% mutator coverage, so the stage passes and says so,
// and an operator reading the table has to be able to tell "the box could not
// measure this" from "nothing survived here".
func TestStats_MutantsSkipIsCountedApartFromARefusal(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	log := stamp(at, "mutants", "/repo", "mutants", "mutants-skipped:gremlins-windows", 0) +
		stamp(at, "mutants", "/repo", "mutants", "mutants-refused:tree-changed", 0)

	s := GateStats(strings.NewReader(log), time.Time{})

	if s.Mutants["skipped:gremlins-windows"] != 1 {
		t.Errorf("mutation stage reasons = %v, want skipped:gremlins-windows=1", s.Mutants)
	}
	if s.Mutants["tree-changed"] != 1 {
		t.Errorf("mutation stage reasons = %v, want tree-changed=1", s.Mutants)
	}
	if got := s.Count("mutants", "red"); got != 0 {
		t.Errorf("mutants red = %d, want 0 — neither line measured a mutant", got)
	}
}
