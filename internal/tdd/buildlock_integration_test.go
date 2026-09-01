package tdd

import (
	"os"
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

	slot, release, ok := acquireBuildSlot(resolveTargetDir(os.Getenv, root), time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the project's build slot")
	}
	defer release()
	// A real holder writes an owner file (runCargoLocked does this
	// automatically; simulated here since this test takes the slot
	// directly) -- the QUEUED-SKIPPED line must NAME it.
	WriteBuildSlotOwner(slot, "cargo nextest run -p other-crate", "/some/other/repo")
	defer RemoveBuildSlotOwner(slot)

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

	_, release, ok := acquireBuildSlot(resolveTargetDir(os.Getenv, root), time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the project's build slot")
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

// TestPrecommit_Mechanical_RejectsWhenNoSlotComesFree pins the outcome that
// used to be a silent hole: ten commits in one gate.log waited out the full
// lock budget, logged queued-skipped, and landed with ZERO tests run. A
// commit the gate could not verify is now REJECTED, loudly, naming the
// holder — the operator can wait, or use --no-verify deliberately, but the
// gate never claims a commit is fine when it never tested it. (A suite
// TIMEOUT still fails open: that is a stopwatch verdict on a run that
// actually happened, not a run that never started.)
func TestPrecommit_Mechanical_RejectsWhenNoSlotComesFree(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	slot, release, ok := acquireBuildSlot(cargoFailFirstTarget(root), time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the gate target's only build slot")
	}
	defer release()
	WriteBuildSlotOwner(slot, "cargo nextest run -p other-crate", "/some/other/repo")
	defer RemoveBuildSlotOwner(slot)

	var invoked bool
	res := Precommit(root, func(Runner, string) SuiteResult {
		invoked = true
		return SuiteResult{Passed: true}
	})

	if !res.Blocked {
		t.Fatalf("a commit the gate could not test must be REJECTED, got: %s", res.Message)
	}
	if !strings.Contains(res.Message, "cargo nextest run -p other-crate") || !strings.Contains(res.Message, "/some/other/repo") {
		t.Fatalf("the rejection must name the holder so the operator knows what to wait for, got: %s", res.Message)
	}
	if invoked {
		t.Fatal("the suite must never run while every slot is busy")
	}
}
