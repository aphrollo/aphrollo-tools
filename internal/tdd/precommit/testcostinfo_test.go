package precommit

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// testCostInfoRepo is a repo whose HEAD has a_test.go with TestOld, and whose
// index adds TestNew to it.
func testCostInfoRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "a_test.go", "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "old")
	write(t, root, "a_test.go", "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n\nfunc TestNew(t *testing.T) {}\n")
	gitDo(t, root, "add", ".")
	return root
}

func testCostInfoRun(tests map[string]float64) func() []testcost.Run {
	return func() []testcost.Run { return []testcost.Run{{Secs: 100, Tests: tests}} }
}

func testCostInfoNoHistory() []testcost.Run { return nil }

func TestTestCostInfoStage_NamesAnAddedTestWhoseRecordedTimeIsOverTheThreshold(t *testing.T) {
	root := testCostInfoRepo(t)
	msg := testCostInfoLine(root, testcost.Run{}, testCostInfoRun(map[string]float64{"m.TestNew": 12.5}))
	for _, want := range []string{"[info]", "TestNew", "12.5s", "docs/test-cost.md", "subprocess"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("message %q is more than one line", msg)
	}
}

func TestTestCostInfoStage_ReadsTheFirstRunWhenNothingIsRecorded(t *testing.T) {
	root := testCostInfoRepo(t)
	first := testcost.Run{Tests: map[string]float64{"m.TestNew": 11}}
	msg := testCostInfoLine(root, first, testCostInfoNoHistory)
	if !strings.Contains(msg, "TestNew") || !strings.Contains(msg, "11s") {
		t.Fatalf("message %q does not name TestNew at 11s", msg)
	}
}

func TestTestCostInfoStage_TheThresholdItselfIsSlowAndJustUnderItIsNot(t *testing.T) {
	root := testCostInfoRepo(t)
	if msg := testCostInfoLine(root, testcost.Run{}, testCostInfoRun(map[string]float64{"m.TestNew": 10})); msg == "" {
		t.Error("a test at exactly 10s was not named")
	}
	if msg := testCostInfoLine(root, testcost.Run{}, testCostInfoRun(map[string]float64{"m.TestNew": 9.99})); msg != "" {
		t.Errorf("a test at 9.99s was named: %q", msg)
	}
}

func TestTestCostInfoStage_SaysNothingOfATestTheCommitDoesNotAdd(t *testing.T) {
	root := testCostInfoRepo(t)
	if msg := testCostInfoLine(root, testcost.Run{}, testCostInfoRun(map[string]float64{"m.TestOld": 40})); msg != "" {
		t.Fatalf("named a test that already existed: %q", msg)
	}
}

func TestTestCostInfoStage_SaysNothingWithoutATiming(t *testing.T) {
	root := testCostInfoRepo(t)
	if msg := testCostInfoLine(root, testcost.Run{}, testCostInfoNoHistory); msg != "" {
		t.Fatalf("spoke with no timing to speak of: %q", msg)
	}
}

// The record is the whole event log: a run that timed the new test already
// answers, and the log is not read for it.
func TestTestCostInfoStage_DoesNotReadTheRecordWhenTheRunTimedTheTest(t *testing.T) {
	root := testCostInfoRepo(t)
	first := testcost.Run{Tests: map[string]float64{"m.TestNew": 11}}
	testCostInfoLine(root, first, func() []testcost.Run {
		t.Error("the history was read though the first run timed the added test")
		return nil
	})
}
