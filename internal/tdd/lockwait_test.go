package tdd

import (
	"os"
	"strings"
	"testing"
	"time"
)

// withShortLockWaitLog makes any wait at all worth logging, so a contention
// test does not have to spend the real threshold to reach the path.
func withShortLockWaitLog(t *testing.T) {
	t.Helper()
	prev := lockWaitLogThreshold
	lockWaitLogThreshold = time.Nanosecond
	t.Cleanup(func() { lockWaitLogThreshold = prev })
}

// Time spent QUEUED is not time spent failing. A commit that waited out the
// lock budget wrote the same kind of line as one whose tests went red, so
// gate stats could not tell a contended box from a broken suite.
func TestPrecommit_LockWaitIsItsOwnLogLine(t *testing.T) {
	withIsolatedBuildLock(t)
	withShortLockWaitLog(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	_, release, ok := acquireBuildSlot(resolvedDevTarget(root), time.Second, "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the gate target's only build slot")
	}
	defer release()

	Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })

	requireLoggedVerdict(t, cfg, "lock-wait")
}

// The line is for a wait worth reporting. An uncontended box takes the lock
// immediately, and logging that would bury the real waits.
func TestPrecommit_ShortLockWaitIsNotLogged(t *testing.T) {
	withIsolatedBuildLock(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })

	if data, err := os.ReadFile(GateLogPath()); err == nil && strings.Contains(string(data), "lock-wait") {
		t.Fatalf("an uncontended run must not report a lock wait:\n%s", data)
	}
}

// The reading an operator needs is "how long did the worst wait get", and a
// wait must never be counted as gate RUN time — that is what made a queued
// box look like a slow suite.
func TestGateStats_LockWaitIsCountedApartFromRunSeconds(t *testing.T) {
	log := strings.Join([]string{
		"2026-09-02T10:00:00Z precommit /repo cargo_test lock-wait 210.0s",
		"2026-09-02T10:00:01Z precommit /repo cargo_test queued-rejected 240.0s",
		"2026-09-02T10:00:02Z precommit /repo cargo_test green 12.0s",
	}, "\n")

	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Count("precommit", "lock-wait") != 1 {
		t.Errorf("lock-wait count = %d, want 1", s.Count("precommit", "lock-wait"))
	}
	if s.LockWaitMax != 210 {
		t.Errorf("LockWaitMax = %v, want 210", s.LockWaitMax)
	}
	if s.Max != 240 {
		t.Errorf("gate seconds max = %v, want 240 — the wait is not a run", s.Max)
	}
	out := RenderGateStats(s)
	if !strings.Contains(out, "lock-wait") || !strings.Contains(out, "210") {
		t.Errorf("the table must show the lock wait and its worst case, got:\n%s", out)
	}
}
