package postedit

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// Edits that land together (a batch of tool calls, two sessions in one lane)
// each ran their own copy of the same package's tests. A run for the same
// tree state and the same command that is already going answers all of them.

// liveSpawner replaces the detached spawner with one that starts a run that
// never finishes, whose process the OS reports as alive, and counts starts.
func liveSpawner(t *testing.T) *int {
	t.Helper()
	at := time.Now()
	restoreStart := SetProcessStartTimeForTest(func(int) (time.Time, bool) { return at, true })
	t.Cleanup(restoreStart)
	started := 0
	restore := SetSpawnPhaseForTest(func(j DeferredJob) (DeferredJob, bool) {
		started++
		j.PID, j.PIDCreatedAt, j.Started = 4000+started, at, at
		saveDeferredJob(j)
		saved, ok := loadDeferredJob(j.Session, j.Project)
		if !ok {
			t.Errorf("setup: the job record for %v did not persist", j.Runner)
		}
		return saved, ok
	})
	t.Cleanup(restore)
	return &started
}

func coalesceRunner(pkg string) Runner { return Runner{Cmd: "go", Args: []string{"test", pkg}} }

func TestRunEditPhases_AnIdenticalRunAlreadyGoing_IsNotStartedAgain(t *testing.T) {
	cases := []struct {
		name         string
		secondRunner Runner
		secondHash   string
		secondSess   string
		wantStarts   int
	}{
		{"same session, same package, same tree state", coalesceRunner("./internal/tdd/merge"), "h1", "s-1", 1},
		{"another session, same package, same tree state", coalesceRunner("./internal/tdd/merge"), "h1", "s-2", 1},
		{"the tree state moved on", coalesceRunner("./internal/tdd/merge"), "h2", "s-2", 2},
		{"another package", coalesceRunner("./internal/tdd/suite"), "h1", "s-2", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			root := t.TempDir()
			EnableDeferredPhases(true)
			t.Cleanup(func() { EnableDeferredPhases(false) })
			started := liveSpawner(t)
			file := root + "/internal/tdd/merge/x.go"

			first := runEditPhases(coalesceRunner("./internal/tdd/merge"), root, file, "head", "h1", "s-1", "", 0)
			second := runEditPhases(c.secondRunner, root, file, "head", c.secondHash, c.secondSess, "", 0)

			if !first.deferred || !second.deferred {
				t.Fatalf("setup: both runs are still going, got first=%+v second=%+v", first, second)
			}
			if *started != c.wantStarts {
				t.Fatalf("runs started = %d, want %d", *started, c.wantStarts)
			}
			if c.wantStarts == 1 && !strings.Contains(tddtest.Pathless(t, second.notice), "already running") {
				t.Fatalf("a run not started again says why, got: %s", second.notice)
			}
		})
	}
}

func TestRunEditPhases_ARunWhoseProcessIsGone_IsStartedAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })
	started := liveSpawner(t)
	file := root + "/internal/tdd/merge/x.go"
	runEditPhases(coalesceRunner("./internal/tdd/merge"), root, file, "head", "h1", "s-1", "", 0)
	t.Cleanup(SetProcessStartTimeForTest(func(int) (time.Time, bool) { return time.Time{}, false }))

	runEditPhases(coalesceRunner("./internal/tdd/merge"), root, file, "head", "h1", "s-2", "", 0)

	if *started != 2 {
		t.Fatalf("a record whose process is gone answers nothing, runs started = %d, want 2", *started)
	}
}
