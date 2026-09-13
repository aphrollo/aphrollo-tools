package tdd

import (
	"strings"
	"testing"
	"time"
)

// A stage that COULD NOT measure is not a stage that found nothing to
// measure. Seventeen merges landed from a Windows box in one session, each
// printing `mutants: gremlins-windows` next to nine green stages, and
// `gate stats` filed every one of them beside `skipped:nothing-to-measure`
// — two lines that mean opposite things (issue #697).
//
// The rule this repo already applies everywhere else — untestedVerdict,
// NoTestsSelected, BuildOnly, zeroTestedNote, the SCOPE UNKNOWN verdict a
// mutant gets when no test in the run's selection could have killed it — is
// that a verdict nobody measured is never presented as a routine outcome.
// The mutation stage is judged by its own rule here.
func TestMeasure_GoOnWindowsSaysTheMergeCarriesNoMutationEvidence(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(setMutantsGOOSForTest("windows"))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.NotMeasured == "" {
		t.Errorf("NotMeasured = %q, want the reason this merge carries no mutation evidence", v.NotMeasured)
	}
	if v.Skipped != "" {
		t.Errorf("Skipped = %q — a measurement that could not run is not a skip", v.Skipped)
	}
	if v.Refused {
		t.Errorf("reporting the gap must not block the merge, got %+v", v)
	}
	for _, want := range []string{"NOT MEASURED", "NO mutation evidence"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("merge-time output never says %q:\n%s", want, log.String())
		}
	}
	if strings.Contains(log.String(), "nothing to measure") {
		t.Errorf("the gap reads as the routine skip it is the opposite of:\n%s", log.String())
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-unmeasured:gremlins-windows") {
		t.Errorf("gate.log files the gap as something else:\n%s", gateLogText(t, cfgDir))
	}
}

// And the two are countable apart in `gate stats`: a week whose mutation row
// is all gap and a week whose mutation row is all empty diffs read the same
// while both land in one `skipped:` column.
func TestStats_CouldNotMeasureIsCountedApartFromNothingToMeasure(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	log := stamp(at, "mutants", "/repo", "mutants", "mutants-unmeasured:gremlins-windows", 0) +
		stamp(at, "mutants", "/repo", "mutants", "mutants-skipped:nothing-to-measure", 0)

	s := GateStats(strings.NewReader(log), time.Time{})

	if s.Mutants["unmeasured:gremlins-windows"] != 1 {
		t.Errorf("mutation stage reasons = %v, want unmeasured:gremlins-windows=1", s.Mutants)
	}
	if s.Mutants["skipped:nothing-to-measure"] != 1 {
		t.Errorf("mutation stage reasons = %v, want skipped:nothing-to-measure=1", s.Mutants)
	}
	if s.Mutants["skipped:gremlins-windows"] != 0 {
		t.Errorf("the gap is still counted as a routine skip: %v", s.Mutants)
	}
	if got := s.Count("mutants", "green"); got != 0 {
		t.Errorf("mutants green = %d, want 0 — neither line measured a mutant", got)
	}
	if out := RenderGateStats(s); !strings.Contains(out, "unmeasured:gremlins-windows=1") {
		t.Errorf("rendered stats never surface the gap:\n%s", out)
	}
}
