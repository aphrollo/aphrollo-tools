package tdd

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// stampedCommit and originHead are 40-hex shas the tests hold constant so the
// rendered line's `built at`/`origin at` shas are literal, not computed by
// the code under test.
const (
	stampedCommit = "ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2"
	originHead    = "15ac7910aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func setStamp(t *testing.T, commit string) {
	t.Helper()
	buildinfo.SetForTest(commit, "2026-09-05T00:00:00Z")
	t.Cleanup(func() { buildinfo.SetForTest("", "") })
}

func stubLsRemote(t *testing.T, fn func(ctx context.Context) (string, error)) {
	t.Helper()
	orig := lsRemoteFn
	lsRemoteFn = fn
	t.Cleanup(func() { lsRemoteFn = orig })
}

// A `go build` run by hand never stamps buildinfo, and a notice that says
// "behind" about an unknown commit is worse than useless — so the seam must
// never even fire.
func TestBinaryBehindLine_SilentWhenUnstamped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, "")

	called := false
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		called = true
		return originHead, nil
	})

	if got := BinaryBehindLine(time.Now()); got != "" {
		t.Errorf("BinaryBehindLine() = %q, want \"\"", got)
	}
	if called {
		t.Error("lsRemoteFn was called for an unstamped binary, want never called")
	}
}

// The exact wording and both truncated shas are the whole point of the line:
// they tell an operator what to run and against what evidence.
func TestBinaryBehindLine_NamesBothShasWhenOriginMoved(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		return originHead, nil
	})

	want := "aphrollo binary is behind origin/main (built at ca47dba, origin at 15ac791): run aphrollo update"
	if got := BinaryBehindLine(time.Now()); got != want {
		t.Errorf("BinaryBehindLine() = %q, want %q", got, want)
	}
}

// A binary already at the tip of main has nothing to say.
func TestBinaryBehindLine_SilentAtHead(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		return stampedCommit, nil
	})

	if got := BinaryBehindLine(time.Now()); got != "" {
		t.Errorf("BinaryBehindLine() = %q, want \"\"", got)
	}
}

// A network call per prompt is a session that pauses for GitHub on every
// turn; the cache is what keeps this to one call an hour.
func TestBinaryBehindLine_AsksTheRemoteOncePerHour(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)

	calls := 0
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		calls++
		return originHead, nil
	})

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	BinaryBehindLine(t0)
	BinaryBehindLine(t0.Add(59 * time.Minute))
	if calls != 1 {
		t.Fatalf("lsRemoteFn called %d times inside the cache window, want 1", calls)
	}

	BinaryBehindLine(t0.Add(61 * time.Minute))
	if calls != 2 {
		t.Fatalf("lsRemoteFn called %d times after the window expired, want 2", calls)
	}
}

// A session start must never hang on the network: a remote that blocks past
// the budget is silence, not an error, and the call returns promptly.
func TestBinaryBehindLine_SilentWhenTheRemoteExceedsTheBudget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	start := time.Now()
	got := BinaryBehindLine(time.Now())
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("BinaryBehindLine() = %q, want \"\"", got)
	}
	if elapsed > 2500*time.Millisecond {
		t.Errorf("BinaryBehindLine took %s, want at most 2.5s", elapsed)
	}
}

// A network outage should cost one 2s budget, not one per session start until
// it clears: a failed lookup backs off for 10 minutes before the remote is
// asked again, and clears the moment a lookup after the backoff succeeds.
func TestBinaryBehindLine_BacksOffTenMinutesAfterAFailedLookup(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)

	calls := 0
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("network unreachable")
		}
		return originHead, nil
	})

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	if got := BinaryBehindLine(t0); got != "" {
		t.Fatalf("BinaryBehindLine() = %q on a failed lookup, want \"\"", got)
	}
	if calls != 1 {
		t.Fatalf("lsRemoteFn called %d times on the first (failing) lookup, want 1", calls)
	}

	if got := BinaryBehindLine(t0.Add(5 * time.Minute)); got != "" {
		t.Fatalf("BinaryBehindLine() = %q inside the failure backoff, want \"\"", got)
	}
	if calls != 1 {
		t.Fatalf("lsRemoteFn called %d times inside the 10m failure backoff, want 1", calls)
	}

	want := "aphrollo binary is behind origin/main (built at ca47dba, origin at 15ac791): run aphrollo update"
	if got := BinaryBehindLine(t0.Add(11 * time.Minute)); got != want {
		t.Fatalf("BinaryBehindLine() = %q after the backoff cleared, want %q", got, want)
	}
	if calls != 2 {
		t.Fatalf("lsRemoteFn called %d times after the backoff cleared, want 2", calls)
	}
}

// gateLogContent reads the whole gate.log the test's isolated CLAUDE_CONFIG_DIR
// wrote, "" when nothing was ever logged (no state dir, or nothing appended).
func gateLogContent(t *testing.T) string {
	t.Helper()
	path := GateLogPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// A command failure (not a timeout) must never be silent in the ledger: it
// is recorded once through appendGateLog, distinguishable from a timeout, and
// the user-facing return value is untouched.
func TestBinaryBehindLine_RecordsAStanddownOnCommandFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		return "", errors.New("network unreachable")
	})

	if got := BinaryBehindLine(time.Now()); got != "" {
		t.Fatalf("BinaryBehindLine() = %q, want \"\"", got)
	}
	log := gateLogContent(t)
	if !strings.Contains(log, "standdown-failed") {
		t.Fatalf("gate.log does not record the command-failure standdown:\n%s", log)
	}
	if strings.Contains(log, "standdown-timeout") {
		t.Fatalf("a command failure must not be recorded as a timeout:\n%s", log)
	}
}

// A timeout must record a DISTINCT reason from a command failure — they are
// different problems (a hung remote vs. a rejected one) with different fixes.
func TestBinaryBehindLine_RecordsAStanddownOnTimeoutDistinctFromFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	if got := BinaryBehindLine(time.Now()); got != "" {
		t.Fatalf("BinaryBehindLine() = %q, want \"\"", got)
	}
	log := gateLogContent(t)
	if !strings.Contains(log, "standdown-timeout") {
		t.Fatalf("gate.log does not record the timeout standdown:\n%s", log)
	}
	if strings.Contains(log, "standdown-failed") {
		t.Fatalf("a timeout must not be recorded as a plain failure:\n%s", log)
	}
}

// Two consecutive calls in the SAME failure state must record the standdown
// only once: a box with no network must not write a line every session
// start, only when the state actually CHANGES.
func TestBinaryBehindLine_RecordsTheStanddownOnceAcrossRepeatedSameFailures(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		return "", errors.New("network unreachable")
	})

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	BinaryBehindLine(t0)
	// Past the 10-minute backoff, so the second call re-hits the network and
	// fails again in the SAME way — the interesting case, not the backoff.
	BinaryBehindLine(t0.Add(11 * time.Minute))

	if n := strings.Count(gateLogContent(t), "standdown-failed"); n != 1 {
		t.Fatalf("standdown-failed recorded %d time(s) across two same-state failures, want 1:\n%s", n, gateLogContent(t))
	}
}

// A recovery followed by a new failure records the standdown again: the
// transition is what gets logged, and going healthy resets it.
func TestBinaryBehindLine_RecordsAgainAfterARecoveryThenANewFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)

	calls := 0
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		calls++
		if calls == 2 {
			return originHead, nil // the recovery, sandwiched between two failures
		}
		return "", errors.New("network unreachable")
	})

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	BinaryBehindLine(t0)                                               // 1: fails
	BinaryBehindLine(t0.Add(11 * time.Minute))                         // 2: recovers
	BinaryBehindLine(t0.Add(11*time.Minute + time.Hour + time.Minute)) // 3: fails again, past the success TTL

	if n := strings.Count(gateLogContent(t), "standdown-failed"); n != 2 {
		t.Fatalf("standdown-failed recorded %d time(s) across fail/recover/fail, want 2:\n%s", n, gateLogContent(t))
	}
}

// A recovery itself is logged too — the Fix's other half of "once per
// transition": going healthy is as much a state change as going unhealthy.
func TestBinaryBehindLine_RecordsRecoveryAfterAFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)

	calls := 0
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("network unreachable")
		}
		return originHead, nil
	})

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	BinaryBehindLine(t0)
	BinaryBehindLine(t0.Add(11 * time.Minute))

	if !strings.Contains(gateLogContent(t), "recovered") {
		t.Fatalf("gate.log does not record the recovery:\n%s", gateLogContent(t))
	}
}

// A lookup that succeeds and has NEVER failed before must write no standdown
// (or recovery) token at all — success is silent in the ledger too, not just
// to the user.
func TestBinaryBehindLine_RecordsNoTokenOnAPlainSuccess(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		return originHead, nil
	})

	BinaryBehindLine(time.Now())

	if log := gateLogContent(t); log != "" {
		t.Fatalf("a plain success must record nothing in gate.log, got:\n%s", log)
	}
}

// The line must reach the session, not just the unit under test.
func TestHandleSessionStart_CarriesTheBehindLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	setStamp(t, stampedCommit)
	stubLsRemote(t, func(ctx context.Context) (string, error) {
		return originHead, nil
	})

	want := "aphrollo binary is behind origin/main (built at ca47dba, origin at 15ac791): run aphrollo update"
	msg := HandleSessionStart([]byte(`{"session_id":"ss-behind"}`))
	if strings.Count(msg, want) != 1 {
		t.Fatalf("session-start message should carry the behind-notice line exactly once:\n%s", msg)
	}
}
