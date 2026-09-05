package tdd

import (
	"bytes"
	"testing"
	"time"
)

// The CI runner never took the box-wide mutation-run lock at all (issue
// #406): a developer typing `aphrollo gate mutants go --diff` by hand on a
// box already running a detached local job oversubscribed it exactly as
// issue #253 describes for two local jobs. The zero value of
// OneJobPerContainer is that hand-typed case, so it must wait.
func TestRunGoMutantsCI_WaitsForTheBoxWideMutationRunLockBeforeRunningGremlins(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)

	started := make(chan struct{})
	prev := goMutantsRunFn
	goMutantsRunFn = func(root, baseSHA, outPath string, workers int, excludeFiles []string) int {
		close(started)
		mustWrite(t, outPath, ciReport)
		return 0
	}
	t.Cleanup(func() { goMutantsRunFn = prev })

	release := acquireMutantsRunLock("holder", "/repo/holder")

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &bytes.Buffer{})
	}()

	// Polled, not checked once after a fixed delay: RunGoMutantsCI runs
	// several real `git` plumbing calls (diffHasMutableGo, the incremental
	// plan) BEFORE it ever reaches the lock, measured at 1.5 s on a loaded
	// box — nowhere near instant. A single check after one fixed wait races
	// that plumbing: on a box loaded enough to push it past the wait, the
	// check fires before gremlins ever could, and the test passes even with
	// the lock skipped entirely (issue #436) — a false negative on exactly
	// the revert this test exists to catch. Watching for the whole window
	// instead means `started` firing at ANY point while this test still
	// holds the lock is a failure, however long the plumbing took to get
	// there.
	deadline := time.Now().Add(5 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-started:
			t.Fatal("gremlins started while another mutation run still held the box-wide lock")
		case <-ticker.C:
		}
	}

	release()

	<-done
	select {
	case <-started:
	default:
		t.Fatal("gremlins never started once the box-wide lock was released")
	}
}

// A hosted GitHub Actions runner is the only job on its container: the lock
// buys it nothing and only risks a block on a stale lock file left by
// something else. OneJobPerContainer skips it entirely, so gremlins runs
// even while another run holds the lock.
//
// Called synchronously, still holding the lock: if OneJobPerContainer did
// not skip it, RunGoMutantsCI would block forever right here, on the lock
// this very test holds — the direct proof, rather than a race against a
// timer.
func TestRunGoMutantsCI_OneJobPerContainerSkipsTheBoxWideLock(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	seen := fakeGremlins(t, ciReport, 0)

	release := acquireMutantsRunLock("holder", "/repo/holder")
	defer release()

	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123", OneJobPerContainer: true}, &bytes.Buffer{})

	if len(*seen) != 1 {
		t.Fatalf("gremlins calls = %d, want 1 — a one-job-per-container run must not wait for the box-wide lock", len(*seen))
	}
}
