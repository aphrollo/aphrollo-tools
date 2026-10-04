package postedit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	return endRunOf(t, root, key, exit, log, "./...")
}

// endRunOf is endRun for the run of one package set.
func endRunOf(t *testing.T, root, key string, exit int, log, pkgs string) PhaseOutcome {
	t.Helper()
	j := DeferredJob{Project: root, Phase: "run", Dir: root, Runner: []string{"go", "test", pkgs}, Log: filepath.Join(t.TempDir(), "run.log")}
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
	if anyLaneRed() {
		t.Fatal("a failed store write must leave no pointer to a red the store does not hold")
	}
	redJobAt(t, root)

	got := stopAt(t, StopHookStop, root, nil)

	if !got.Block || !strings.Contains(got.Reason, "tests::a_breaks") {
		t.Fatalf("verdict = %+v, want the session read's block naming tests::a_breaks", got)
	}
}

// A store red and the session's own record of the same run are one red: the
// block that tells the first tells the second, and the next Stop allows.
func TestDecideStop_ABlockForAStoreRedConsumesTheSessionsRecordOfTheSameRun(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)
	redJobAt(t, root)

	first := stopAt(t, StopHookStop, root, nil)
	second := stopAt(t, StopHookStop, root, nil)

	if !first.Block || !strings.Contains(first.Reason, "TestBreaks") || !strings.Contains(first.Reason, "tests::a_breaks") {
		t.Fatalf("first = %+v, want one block carrying the store's red and the session's", first)
	}
	if second.Block {
		t.Fatalf("second = %+v, want an allow: the session's record of the same run was told with the first block", second)
	}
}

// A payload with no session has nobody to tell: the store path never blocks it,
// however red the tree.
func TestLaneRedVerdict_NoSessionNeverBlocks(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)

	if got := laneRedVerdict("", root); got.Block {
		t.Fatalf("verdict = %+v, want an allow", got)
	}
}

// A green of one package set hides no red of another on the same tree, and a
// newer run of one unit clears only that unit's pointer.
func TestDecideStop_AGreenOfOnePackageHidesNoRedOfAnother(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRunOf(t, root, "aaa111", 1, failingGoLog, "./pkgB")
	endRunOf(t, root, "aaa111", 0, "ok  \tx\t0.01s\n", "./pkgA")

	got := stopAt(t, StopHookStop, root, nil)

	if !got.Block || !strings.Contains(got.Reason, "./pkgB") || strings.Contains(got.Reason, "./pkgA") {
		t.Fatalf("verdict = %+v, want a block naming ./pkgB alone", got)
	}
}

// A green whose store write failed still clears the red of its unit: the
// pointer follows the verdict, not the write.
func TestRecordPhaseVerdict_AGreenWhoseStoreWriteFailedStillClearsTheRedPointer(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)
	if !anyLaneRed() {
		t.Fatal("setup: the red left no pointer")
	}
	prev := openRunStoreFn
	openRunStoreFn = func(string) (*store.Store, error) { return nil, errors.New("disk full") }
	t.Cleanup(func() { openRunStoreFn = prev })

	endRun(t, root, "bbb222", 0, "ok  \tx\t0.01s\n")

	if anyLaneRed() {
		t.Fatal("the red pointer outlived a newer green of its unit")
	}
}

// A pointer to a red of a tree the lane left is removed by the Stop that finds
// it stale, so the tree key is read once for a moved tree, not at every Stop.
func TestDecideStop_AStalePointerIsRemovedAndNotReadAgain(t *testing.T) {
	root, tree := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)
	*tree = "bbb222"
	reads := 0
	prev := worktreeKeyFn
	worktreeKeyFn = func(string) (string, error) { reads++; return *tree, nil }
	t.Cleanup(func() { worktreeKeyFn = prev })

	stopAt(t, StopHookStop, root, nil)
	stopAt(t, StopHookStop, root, nil)

	if anyLaneRed() {
		t.Fatal("the stale pointer was left in place")
	}
	if reads != 1 {
		t.Fatalf("the tree key was read %d times over two Stops, want 1", reads)
	}
}

// The state sweep drops told-marks past their keep and the pointers of a
// worktree that is gone, and nothing else.
func TestSweepLaneState_DropsOldMarksAndPointersOfVanishedWorktrees(t *testing.T) {
	root, _ := laneAt(t, "aaa111")
	endRun(t, root, "aaa111", 1, failingGoLog)
	gone := filepath.Join(t.TempDir(), "removed-lane")
	writeLaneRed(laneRedPointer{Root: gone, Key: "ccc333", Unit: "x|go test"})
	markRedSeen("old-session", "aaa111")
	markRedSeen("fresh-session", "bbb222")
	old := time.Now().Add(-2 * seenKeep)
	if err := os.Chtimes(seenMark("old-session", "aaa111"), old, old); err != nil {
		t.Fatal(err)
	}

	sweepLaneState(time.Now())

	if _, err := os.Stat(seenMark("old-session", "aaa111")); err == nil {
		t.Error("a told-mark past its keep survived the sweep")
	}
	if _, err := os.Stat(seenMark("fresh-session", "bbb222")); err != nil {
		t.Error("a fresh told-mark was swept")
	}
	pointers := readLaneReds()
	if len(pointers) != 1 || pointers[0].Root != root {
		t.Fatalf("pointers = %+v, want only the live worktree's", pointers)
	}
}

// A run's unit is named from the repository root and carries its package set,
// without the -timeout the deferral adds.
func TestRunUnitOf_IsRelativeToTheRepositoryAndCarriesThePackages(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitDo(t, repo, "init", "-q")
	project := filepath.Join(repo, "svc")
	mustWrite(t, filepath.Join(project, "go.mod"), "module svc\n")

	got := runUnitOf(DeferredJob{Project: project, Runner: []string{"go", "test", "-timeout=9m", "./pkgA"}})

	if want := "svc|go test ./pkgA"; got != want {
		t.Fatalf("unit = %q, want %q", got, want)
	}
}
