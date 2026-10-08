package merge

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

const mergeCostStream = `{"Action":"pass","Package":"m/a","Test":"TestSlow","Elapsed":12.5}
{"Action":"pass","Package":"m/a","Elapsed":13}
`

func mergeCostEvents(root string) []core.Event {
	var out []core.Event
	for _, e := range core.ReadEvents(root) {
		if e.Kind == testcost.EventKind {
			out = append(out, e)
		}
	}
	return out
}

// mergeCostLane is a lane whose merge the gate judges (it declares
// mutants-at-merge) with the mutation tool answering "caught" for the one
// mutant, so a green suite lands.
func mergeCostLane(t *testing.T) string {
	t.Helper()
	t.Cleanup(SetFreeSpaceForTest(999, true))
	t.Cleanup(setMutantsJobsForTest(1, "pinned"))
	root, _ := prGateLane(t)
	declareMutantsAtMergeCommitted(t, root)
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, argvValueOf(t, c.Argv, "--output"), MutantOutcome{
			File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace - with +", Package: "a", Status: "caught"})
		return 0, nil
	})
	return root
}

// The merge gate already runs the suite on the merged tree; what that run
// cost is written down once, as an aggregate, so the law, the report and the
// info line read a history instead of guessing.
func TestGatePRMerge_RecordsTheMergedTreeSuiteCostOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mergeCostLane(t)

	var seen []gateRun
	res := SuiteResult{Passed: true, Duration: 90 * time.Second, GoTestJSON: mergeCostStream}
	if err := GatePRMerge(root, "", recordRuns(&seen, res), io.Discard); err != nil {
		t.Fatalf("a green merged tree must land: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("the gate ran no suite, so the test proves nothing about recording one")
	}

	events := mergeCostEvents(root)
	if len(events) != 1 {
		t.Fatalf("%d suite.cost events, want exactly one per merge gate", len(events))
	}
	got := testcost.FromDetail(events[0].Secs, time.Time{}, events[0].Detail)
	if got.Tests["m/a.TestSlow"] != 12.5 {
		t.Errorf("recorded tests = %v, want m/a.TestSlow at 12.5", got.Tests)
	}
	if events[0].Secs != float64(len(seen))*90 {
		t.Errorf("secs = %v, want %v (90s for each of the %d suites run)", events[0].Secs, float64(len(seen))*90, len(seen))
	}
}

// A refused merge never lands, and the run it stopped at is not what the
// suite costs.
func TestGatePRMerge_RecordsNothingWhenTheMergeIsRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mergeCostLane(t)

	var seen []gateRun
	if err := GatePRMerge(root, "", recordRuns(&seen, SuiteResult{Passed: false, Output: "FAIL"}), io.Discard); err == nil {
		t.Fatal("a red merged tree landed")
	}
	if n := len(mergeCostEvents(root)); n != 0 {
		t.Fatalf("%d suite.cost events after a refused merge, want none", n)
	}
}
