package postedit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// laneAt gives a test its own state root and a tree it names itself: the store
// the Stop checks read lives under it, and key is the worktree key every call
// sees until the test moves it.
func laneAt(t *testing.T, key string) (root string, tree *string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root = mkProject(t, "go.mod")
	tree = &key
	prev := worktreeKeyFn
	worktreeKeyFn = func(string) (string, error) { return *tree, nil }
	t.Cleanup(func() { worktreeKeyFn = prev })
	return root, tree
}

const failingGoLog = "--- FAIL: TestBreaks (0.00s)\nFAIL\nFAIL\tx\t0.01s\n"

// endRun is what the run's wrapper does when its tests end on tree key: record
// the verdict. It answers the outcome the wrapper would write for the hook.
func endRun(t *testing.T, root, key string, exit int, log string) PhaseOutcome {
	t.Helper()
	j := DeferredJob{Project: root, Phase: "run", Dir: root, Runner: []string{"go", "test", "./..."}, Log: filepath.Join(t.TempDir(), "run.log")}
	mustWrite(t, j.Log, log)
	out := PhaseOutcome{ExitCode: exit, Seconds: 1}
	out.TreeKey, out.StoreResult = key, recordPhaseVerdict(j, out, key)
	return out
}

func stopAt(t *testing.T, event StopEvent, root string, extra map[string]any) StopVerdict {
	t.Helper()
	fields := stopFields(root)
	for k, v := range extra {
		fields[k] = v
	}
	fixture := "stop.json"
	if event == StopHookSubagentStop {
		fixture = "subagentstop.json"
	}
	return DecideStop(event, stopPayload(t, fixture, fields))
}

// A red the store holds for the lane's current tree, that this session has not
// been told of, blocks the turn once and names the failing test; the block is
// the telling, so the next check allows.
func TestDecideStop_BlocksOnceOnAnUnseenRedOfTheCurrentTree(t *testing.T) {
	for _, event := range []StopEvent{StopHookStop, StopHookSubagentStop} {
		t.Run(string(event), func(t *testing.T) {
			root, _ := laneAt(t, "aaa111")
			endRun(t, root, "aaa111", 1, failingGoLog)

			got := stopAt(t, event, root, nil)

			if !got.Block || !strings.Contains(got.Reason, "TestBreaks") {
				t.Fatalf("verdict = %+v, want a block naming TestBreaks", got)
			}
			if again := stopAt(t, event, root, nil); again.Block {
				t.Fatalf("second check = %+v, want an allow: the red was told once", again)
			}
		})
	}
}

// The session whose hook printed the red line was told of it.
func TestDecideStop_AllowsARedThisSessionWasTold(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	out := endRun(t, root, "aaa111", 1, failingGoLog)
	markOutcomeSeen(stopSession, out)

	if got := stopAt(t, StopHookStop, root, nil); got.Block {
		t.Fatalf("verdict = %+v, want an allow: this session read that red", got)
	}
}

// A red of a tree the lane has since left is not on the current key: newer
// edits supersede it.
func TestDecideStop_AllowsARedOfAnOlderTree(t *testing.T) {
	root, tree := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)
	*tree = "bbb222"

	if got := stopAt(t, StopHookStop, root, nil); got.Block {
		t.Fatalf("verdict = %+v, want an allow: the red is about tree aaa111, the lane is at bbb222", got)
	}
}

// A newer green on the lane's tree removes the red from the Stop check's view.
func TestDecideStop_AllowsOnceANewerRunWasGreen(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)
	endRun(t, root, "bbb222", 0, "ok  \tx\t0.01s\n")

	if got := stopAt(t, StopHookStop, root, nil); got.Block {
		t.Fatalf("verdict = %+v, want an allow: the last run of the project was green", got)
	}
}

// A run that was not tested is no red: the check allows.
func TestDecideStop_AllowsATreeWhoseRunWasNotTested(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	j := DeferredJob{Project: root, Phase: "run", Dir: root, Runner: []string{"go", "test"}, Log: filepath.Join(t.TempDir(), "run.log")}
	mustWrite(t, j.Log, "")
	recordPhaseVerdict(j, PhaseOutcome{ExitCode: 1, Inconclusive: "SKIPPED — no memory"}, "aaa111")

	if got := stopAt(t, StopHookStop, root, nil); got.Block {
		t.Fatalf("verdict = %+v, want an allow: not tested is not red", got)
	}
}

func TestDecideStop_StoreRedStillYieldsToStopHookActive(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)

	if got := stopAt(t, StopHookStop, root, map[string]any{"stop_hook_active": true}); got.Block {
		t.Fatalf("verdict = %+v, want an allow: never block twice in a row", got)
	}
	if got := stopAt(t, StopHookStop, root, nil); !got.Block {
		t.Fatal("the active-stop allow must not have consumed the red")
	}
}

// A store that cannot be written leaves the session's own job record, which the
// check still reads as before.
func TestDecideStop_FallsBackToTheSessionReadWhenTheStoreWriteFailed(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	prev := openRunStoreFn
	openRunStoreFn = func(string) (*store.Store, error) { return nil, errors.New("disk full") }
	t.Cleanup(func() { openRunStoreFn = prev })
	endRun(t, root, "aaa111", 1, failingGoLog)
	if _, err := os.Stat(laneRedDir()); err == nil {
		t.Fatal("a failed store write must leave no pointer to a red the store does not hold")
	}
	redJobAt(t, root)

	got := stopAt(t, StopHookStop, root, nil)

	if !got.Block || !strings.Contains(got.Reason, "tests::a_breaks") {
		t.Fatalf("verdict = %+v, want the session read's block naming tests::a_breaks", got)
	}
}
