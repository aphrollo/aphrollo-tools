package mutation

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The run reads its time through commitNowFn, so what it does with time is
// stated here without waiting: each reading of the stepping clock is one step
// after the last.

// steppingClock replaces commitNowFn with a clock that advances by step at
// every reading, and restores it after the test.
func steppingClock(t *testing.T, step time.Duration) {
	t.Helper()
	var readings atomic.Int64
	prev := commitNowFn
	commitNowFn = func() time.Time {
		return time.Unix(0, 0).Add(time.Duration(readings.Add(1)) * step)
	}
	t.Cleanup(func() { commitNowFn = prev })
}

// The run gets the budget less what the stage already spent on the lock and on
// reading git: with a 90 s budget and 10 s spent, it is given about 80 s and
// never more than the budget.
func TestMutantsAtCommitStage_TheRunIsGivenTheBudgetLessWhatTheStageSpent(t *testing.T) {
	_, root := commitStage(t, "")
	steppingClock(t, 10*time.Second)
	// The run settles its mutants on parallel workers, so the first reading
	// is taken under a lock; the stage joins them before it returns.
	var mu sync.Mutex
	var left time.Duration
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, _ string, _ []string, _ []string, _ io.Writer) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if deadline, ok := ctx.Deadline(); ok && left == 0 {
			left = time.Until(deadline)
		}
		return 0, nil
	}
	t.Cleanup(func() { resolveExecFn = prev })

	mutantsAtCommitStage("precommit", root)

	mu.Lock()
	defer mu.Unlock()
	// The coverage build reads the clock for its own timing, and the stage reads it
	// again for the budget left, so both count as time the stage spent.
	if left <= 50*time.Second || left > 60*time.Second {
		t.Errorf("the run's deadline was %s away, want within (50s, 60s] of a 90s budget with 30s spent", left)
	}
}

func TestBuildTestMap_ReportsItsTimeRoundedToATenthOfASecond(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	steppingClock(t, 1234*time.Millisecond)
	var log bytes.Buffer
	if _, _, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "in 1.2s") {
		t.Errorf("log = %q, want the counts and the time rounded to a tenth", log.String())
	}
}

func TestCommitVerdict_RoundsTimesToATenthOfASecond(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	caught := commitRun{
		Mutant:  kindMutant,
		Outcome: MutantOutcome{File: "gate/gate.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY", Status: "caught"},
		Took:    5678 * time.Millisecond,
	}
	stderr := captureStderr(t, func() {
		commitVerdict("precommit", t.TempDir(), MutantsConfig{}, []commitRun{caught}, 1234*time.Millisecond)
	})
	if !strings.Contains(stderr, "(1.2s, slowest mutant 5.7s)") {
		t.Errorf("stderr = %q, want both times rounded to a tenth", stderr)
	}
}

// ratchet: test_removed TestRunMutantsTestMap_ReportsItsTimeRoundedToATenthOfASecond: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
