package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunGitShim_UnchangingIndexLockStopsWaitingAndNamesTheRemedy is issue
// #608. Killing a git process leaves a zero-byte index.lock nothing holds;
// the shim then spent its ENTIRE 20-minute budget polling it and gave up
// with "holder: (unknown)", so every git verb in that checkout was dead for
// twenty minutes over a file whose removal took effect instantly. A lock
// file records no pid, so the shim cannot prove the holder is gone and must
// not delete it -- but it must stop guessing in SECONDS and hand the
// operator the file and the fix.
func TestRunGitShim_UnchangingIndexLockStopsWaitingAndNamesTheRemedy(t *testing.T) {
	withDirectGitShim(t)
	commonDir := gitCommonDirEnv(t)
	indexLockPath := commonDir + "/index.lock"
	if err := os.WriteFile(indexLockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := gitShimConfig{
		waitBudget:     30 * time.Second,
		pollInterval:   time.Millisecond,
		indexLockGrace: 50 * time.Millisecond,
		realGit:        gitStub(t),
	}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, &stderr, cfg)
	waited := time.Since(start)

	if code != exGitTempFail {
		t.Fatalf("exit = %d, want %d, stderr=%s", code, exGitTempFail, stderr.String())
	}
	if waited >= cfg.waitBudget {
		t.Fatalf("waited %s of a %s budget — an unchanging index.lock must end the wait early", waited, cfg.waitBudget)
	}
	out := stderr.String()
	if !strings.Contains(out, indexLockPath) {
		t.Fatalf("the diagnostic must name the exact file %q, got: %s", indexLockPath, out)
	}
	if !strings.Contains(out, "rm ") {
		t.Fatalf("the diagnostic must name the remedy that clears it, got: %s", out)
	}
	if _, err := os.Stat(indexLockPath); err != nil {
		t.Fatalf("the shim must never remove a lock it cannot prove is unheld: %v", err)
	}
}

// TestGitIndexLockWatch_OnlyAnUnchangedLockEndsTheWait decides how
// conservative #608's rule is, on a clock the test owns rather than on real
// time. A lock a live git is still writing into keeps every bit of its
// protection: each write restarts the window. Only a lock that has not
// changed AT ALL for the whole grace period ends the wait, and an absent
// one never does.
func TestGitIndexLockWatch_OnlyAnUnchangedLockEndsTheWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.lock")
	clock := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	w := gitIndexLockWatch{now: func() time.Time { return clock }}
	const grace = 15 * time.Second

	// No lock at all: nothing to end the wait over.
	if _, stalled := w.stallLine(path, grace, true); stalled {
		t.Fatal("an absent index.lock must never end the wait")
	}

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A live git writing its new index: every observation differs from the
	// last, so the window restarts and the wait continues however long the
	// write takes.
	for i := 0; i < 5; i++ {
		write(strings.Repeat("x", i+1))
		clock = clock.Add(grace * 2)
		if _, stalled := w.stallLine(path, grace, true); stalled {
			t.Fatalf("write %d: a lock that changed since the last look must keep its protection", i)
		}
	}

	// The writer stops. The first look after that starts the window; it must
	// not end the wait until the full grace has passed unchanged.
	clock = clock.Add(grace - time.Second)
	if _, stalled := w.stallLine(path, grace, true); stalled {
		t.Fatalf("unchanged for %s of a %s grace — too early to stop waiting", grace-time.Second, grace)
	}
	clock = clock.Add(2 * time.Second)
	line, stalled := w.stallLine(path, grace, true)
	if !stalled {
		t.Fatalf("unchanged for %s of a %s grace — the wait must end", grace+time.Second, grace)
	}
	if !strings.Contains(line, path) || !strings.Contains(line, "rm ") {
		t.Fatalf("the diagnostic must name the file and the remedy, got: %s", line)
	}

	// The advisory lock, not index.lock, is what this invocation is waiting
	// on: index.lock is not the blocker, so its window is dropped entirely.
	if _, stalled := w.stallLine(path, grace, false); stalled {
		t.Fatal("index.lock must not end a wait it is not the blocker for")
	}
	clock = clock.Add(2 * grace)
	if _, stalled := w.stallLine(path, grace, true); stalled {
		t.Fatal("the window must restart from the first look at which index.lock blocks again")
	}
}
