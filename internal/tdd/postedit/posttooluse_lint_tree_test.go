package postedit

import (
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
	"github.com/aphrollo/aphrollo-tools/internal/run/runtest/chaintool"
)

// A lint past its budget is reported as timed out and the whole tree it
// started ends with it: golangci-lint starts the go command, which starts the
// compiler, and a lint left running after the edit hook gave up on it holds
// the box's memory with nobody reading its output.
func TestRunLintWithin_BudgetEndsTheLintersWholeProcessTree(t *testing.T) {
	pidFile, _ := chaintool.Install(t, "golangci-lint")

	_, timedOut := runLintWithin(t.TempDir(), []string{"run"}, 8*time.Second)

	if !timedOut {
		t.Fatal("a run past its budget was not reported as timed out")
	}
	runtest.RequireTreeGone(t, pidFile, 3)
}
