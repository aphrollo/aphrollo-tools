package workspace

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// offLaneWorld is a merge world whose lane is in the off arm and whose GitHub checks are gh.
func offLaneWorld(t *testing.T, gh CIStatus) *ciWorld {
	t.Helper()
	w := newCIWorld(t, tdd.CIAuto, gh)
	prev, prevEsc := laneIsOff, escapeClosureBeforeMerge
	laneIsOff = func(string) bool { return true }
	escapeClosureBeforeMerge = func(string, int, io.Writer) error {
		t.Error("the escape-closure judgment ran for an off lane")
		return nil
	}
	t.Cleanup(func() { laneIsOff, escapeClosureBeforeMerge = prev, prevEsc })
	return w
}

// An off lane merges like any other, with every local gate skipped and no CI condition
// of aphrollo's: GitHub's checks are not read, and neither the gate nor the local CI runs.
func TestMerge_AnOffLaneMergesWithEveryLocalGateSkippedAndNoCIRead(t *testing.T) {
	w := offLaneWorld(t, CIStatus{State: "red", Failing: 4, SHA: "abc", Checks: 4})
	out, err := applyMerge(t, "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if w.merges != 1 || w.gateRuns != 0 || w.localRuns != 0 || w.ghReads != 0 {
		t.Errorf("merges %d, gate runs %d, local CI runs %d, GitHub reads %d; want 1, 0, 0, 0", w.merges, w.gateRuns, w.localRuns, w.ghReads)
	}
	if !strings.Contains(out, "merged PR #5") {
		t.Errorf("output = %q, want the merge confirmation", out)
	}
}

// The control: the same green checks on a lane that is on still run the gate.
func TestMerge_AnOnLaneStillRunsTheGate(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "green", SHA: "abc", Checks: 3})
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if w.gateRuns != 1 {
		t.Errorf("gate runs = %d, want 1 for an on lane", w.gateRuns)
	}
}

// The measurement a PR opens behind is a gate of aphrollo's: an off lane opens its PR
// unmeasured, with nothing said about it.
func TestPR_AnOffLaneOpensWithoutTheMutationMeasurement(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)
	prev := laneIsOff
	laneIsOff = func(string) bool { return true }
	t.Cleanup(func() { laneIsOff = prev })

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s", err, out.String())
	}
	if fake.calls != 0 || !*created {
		t.Errorf("measurements = %d, PR created = %v; want 0 and true for an off lane", fake.calls, *created)
	}
	if strings.Contains(out.String(), "mutants") {
		t.Errorf("an off lane's PR open said something about mutants: %q", out.String())
	}
}
