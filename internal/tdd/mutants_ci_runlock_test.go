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

	// A bounded probe of the SAME lock this test holds: it can only fail
	// while the test holds the lock, and by the time it returns, gremlins
	// would already have started had RunGoMutantsCI not waited for it too.
	// The deadline is generous rather than tight, unlike the local job's own
	// version of this test: RunGoMutantsCI runs several real `git` plumbing
	// calls (diffHasMutableGo, the incremental plan) BEFORE it ever reaches
	// the lock, measured at 1.5 s on a loaded box — nowhere near instant, so
	// a short probe window would let gremlins simply not have gotten there
	// yet regardless of whether the lock is doing anything at all.
	if _, gotLock := acquireMutantsRunLockWithDeadline("probe", "/repo/probe", 5*time.Second); gotLock {
		t.Fatal("setup: a probe acquired the box-wide lock this test still holds")
	}
	select {
	case <-started:
		t.Fatal("gremlins started while another mutation run still held the box-wide lock")
	default:
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
