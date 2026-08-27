package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestPostEdit_QueuedSkipped_WhenBuildLockHeld pins the PostEdit side of task
// A3: while another cargo build already holds the machine-wide lock, a cargo
// project's PostEdit run must not even attempt the build — it reports
// QUEUED-SKIPPED (A2's format) and, critically, must NOT stamp a timeout:
// a lock contention is a completely different fact from "the suite ran and
// blew its budget", and conflating the two would poison the timeout-streak
// backoff over lock contention that has nothing to do with this project's
// suite being slow.
func TestPostEdit_QueuedSkipped_WhenBuildLockHeld(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")

	release, ok := acquireBuildLock(time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the build lock")
	}
	defer release()
	// A real holder writes an owner file (runCargoLocked does this
	// automatically; simulated here since this test holds the lock directly
	// via acquireBuildLock) -- the QUEUED-SKIPPED line must NAME it.
	writeBuildLockOwner("cargo nextest run -p other-crate", "/some/other/repo")
	defer removeBuildLockOwner()

	var invoked bool
	run := func(Runner, string) SuiteResult {
		invoked = true
		return SuiteResult{Passed: true, Output: "ok"}
	}

	got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), run)
	if !strings.Contains(got, "QUEUED-SKIPPED") {
		t.Fatalf("expected a QUEUED-SKIPPED line, got: %s", got)
	}
	if !strings.Contains(got, "cargo nextest run -p other-crate") || !strings.Contains(got, "/some/other/repo") {
		t.Fatalf("expected the QUEUED-SKIPPED line to name the holder's cmd/cwd, got: %s", got)
	}
	if invoked {
		t.Fatal("the suite must never run while the build lock is held by someone else")
	}

	s, _ := loadSession("sess-post")
	if ps := s.ByProject[root]; ps.TimeoutStreak != 0 {
		t.Fatalf("a queued-skip must NOT stamp a timeout, got streak=%d", ps.TimeoutStreak)
	}
}

// TestPostEdit_NonCargoRunner_NeverTakesTheBuildLock pins the scope limit
// (task A3 point 3): go/pytest/vitest suites don't saturate the box the way
// a cargo build does, so they must run REGARDLESS of who holds the cargo
// build lock — a go-only session must never queue behind an unrelated cargo
// build.
func TestPostEdit_NonCargoRunner_NeverTakesTheBuildLock(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")

	release, ok := acquireBuildLock(time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the build lock")
	}
	defer release()

	got := PostEdit(postPayload("Edit", root+"/widget.go"), fakeRun(true, "ok\nPASS"))
	if strings.Contains(got, "QUEUED-SKIPPED") {
		t.Fatalf("a go project must never queue behind the cargo build lock, got: %s", got)
	}
	if !strings.Contains(got, "→ green") {
		t.Fatalf("expected the run to actually execute and report green, got: %s", got)
	}
}

// TestPrecommit_Mechanical_QueuedSkipped_WhenBuildLockHeld pins the
// Precommit side: a cargo mechanical stage that can't get the build lock
// within its budget reports QUEUED-SKIPPED with the seconds waited, fails
// OPEN (never blocks — lock contention is not a test failure), and is never
// silent about it (Message is set, matching the same fail-open contract as
// a suite timeout).
func TestPrecommit_Mechanical_QueuedSkipped_WhenBuildLockHeld(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	release, ok := acquireBuildLock(time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the build lock")
	}
	defer release()
	writeBuildLockOwner("cargo nextest run -p other-crate", "/some/other/repo")
	defer removeBuildLockOwner()

	var invoked bool
	run := func(Runner, string) SuiteResult {
		invoked = true
		return SuiteResult{Passed: true}
	}

	res := Precommit(root, run)
	if res.Blocked {
		t.Fatalf("lock contention must fail OPEN, never block: %s", res.Message)
	}
	if res.Message == "" || !strings.Contains(res.Message, "QUEUED-SKIPPED") {
		t.Fatalf("expected a non-empty QUEUED-SKIPPED Message, got %q", res.Message)
	}
	if !strings.Contains(res.Message, "cargo nextest run -p other-crate") || !strings.Contains(res.Message, "/some/other/repo") {
		t.Fatalf("expected the QUEUED-SKIPPED Message to name the holder's cmd/cwd, got: %s", res.Message)
	}
	if invoked {
		t.Fatal("the mechanical suite must never run while the build lock is held by someone else")
	}
}
