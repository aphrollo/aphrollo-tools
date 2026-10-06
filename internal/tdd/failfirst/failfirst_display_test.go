package failfirst

import (
	"fmt"
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func displayRunCmd(n int) (cmd string, names []string) {
	for i := range n {
		names = append(names, fmt.Sprintf("TestCase%02d", i))
	}
	return "go test ./internal/tdd/postedit -run ^(" + strings.Join(names, "|") + ")$", names
}

// The fail-first line of a commit that adds many tests printed the -run
// expression of every one of them, twice (the red run and the green run). The
// line now counts them and names one; the whole command is what the run's
// retained output and the gate log's event keep, so `aphrollo gate output`
// still prints every name.
func TestListedRunCmd_AWideTestListIsACountAndOneName(t *testing.T) {
	cmd, _ := displayRunCmd(13)

	got := listedRunCmd(cmd)

	if want := "go test ./internal/tdd/postedit -run <13 tests, e.g. TestCase00>"; got != want {
		t.Errorf("listedRunCmd = %q, want %q", got, want)
	}
}

func TestListedRunCmd_ShortListsAndOtherCommandsAreLeftAlone(t *testing.T) {
	short, _ := displayRunCmd(3)
	for _, cmd := range []string{short, "go test ./...", "cargo test -p particles --test gpu_parity", "pytest -k a or b or c or d"} {
		if got := listedRunCmd(cmd); got != cmd {
			t.Errorf("listedRunCmd(%q) = %q, want it unchanged", cmd, got)
		}
	}
}

// The green half prints through greenRefusal; its line carries the short form
// while the retained run keeps the whole command.
func TestGreenRefusal_PrintsAWideTestListAsACount(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	cmd, _ := displayRunCmd(13)

	stderr := tddtest.CaptureStderr(t, func() {
		greenRefusal(root, cmd, greenProof{ran: true, res: SuiteResult{Passed: true, Output: "ok\n"}})
	})

	if !strings.Contains(stderr, "-run <13 tests, e.g. TestCase00> in ") || strings.Contains(stderr, "TestCase05") {
		t.Errorf("the green line:\n%s", stderr)
	}
	retained, err := RetainedSuiteOutput(root)
	if err != nil || !strings.Contains(retained, "TestCase12") {
		t.Errorf("the retained run must keep the whole command (err %v):\n%s", err, retained)
	}
}
