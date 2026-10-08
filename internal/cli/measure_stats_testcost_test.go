package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The suite's cost at merge shows beside the other measures: the merges the
// gate recorded and the slowest tests, so a suite that drifts slow is seen
// without opening the report.
func TestStats_TextPrintsTheTestCostTheMergeGateRecorded(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{
		2 * time.Hour: {Kind: "suite.cost", Lane: "lane/a", Secs: 400, Detail: map[string]string{"t:m/a.TestSlow": "12.5"}},
		time.Hour:     {Kind: "suite.cost", Lane: "lane/a", Secs: 420, Detail: map[string]string{"t:m/a.TestSlow": "14"}},
	})
	code, out, errOut := runStatsCmd(t, "--repo", repo)
	if code != 0 {
		t.Fatalf("stats exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"Test cost (merge-gate suite runs recorded: 2", "m/a.TestSlow", "+1.5s"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output lacks %q:\n%s", want, out)
		}
	}
}

func TestStats_TheAbReadoutDoesNotCarryTheTestCostSection(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: {Kind: "suite.cost", Secs: 400}})
	_, out, _ := runStatsCmd(t, "--repo", repo, "--ab")
	if strings.Contains(out, "Test cost") {
		t.Errorf("--ab replaces the report, yet printed the test cost section:\n%s", out)
	}
}
