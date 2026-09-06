package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeGateLog plants a gate.log under the per-test state dir, creating it.
func writeGateLog(t *testing.T, text string) {
	t.Helper()
	path := GateLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A number nobody reads is not a ratchet. The digest is the one line a
// session start spends on pipeline health, and it carries the escape count
// so the debt stays visible without anybody typing a command.
func TestWeeklyDigestReportsRatesDeniesAndOpenEscapes(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	now := time.Now().UTC()
	stamp := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	log := strings.Join([]string{
		stamp(time.Hour) + " postedit /r go test ./... green 1.0s",
		stamp(2*time.Hour) + " postedit /r go test ./... green 1.0s",
		stamp(3*time.Hour) + " postedit /r go test ./... red 1.0s",
		stamp(4*time.Hour) + " postedit /r go test ./... queued-skipped 0.0s",
		stamp(5*time.Hour) + " preedit /r a_test.go pretooluse-denied:test-sleep 0.0s",
		stamp(6*time.Hour) + " session /r s override-off 0.0s",
		stamp(20*24*time.Hour) + " postedit /r go test ./... red 1.0s",
		"",
	}, "\n")
	writeGateLog(t, log)
	if err := appendEscape(EscapeRecord{
		ID: "a", Kind: EscapeKind, Reason: "x",
		At: now.Add(-11 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	line := weeklyDigest(now)
	// green 2 of the 4 runs inside the window, queued-skipped 1 of 4; the
	// 20-day-old red is outside it. denies and overrides are counted apart:
	// a refusal and a waiver are different facts.
	for _, want := range []string{
		"last 7d", "green 50%", "queued-skipped 25%",
		"denies 1", "overrides 1", "open escapes 1", "oldest 11d",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("digest %q does not carry %q", line, want)
		}
	}
}

// Once per seven days: a health line on every session start is a line nobody
// reads by the third one.
func TestSessionStartPrintsTheDigestOncePerWeek(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	writeGateLog(t, time.Now().UTC().Format(time.RFC3339)+" postedit /r go test ./... green 1.0s\n")

	first := maybeWeeklyDigest(time.Now())
	if !strings.Contains(first, "last 7d") {
		t.Fatalf("the first session start of the week prints the digest, got %q", first)
	}
	if second := maybeWeeklyDigest(time.Now()); second != "" {
		t.Fatalf("the next session start stays quiet, got %q", second)
	}
	if later := maybeWeeklyDigest(time.Now().Add(8 * 24 * time.Hour)); !strings.Contains(later, "last 7d") {
		t.Fatalf("a week later it prints again, got %q", later)
	}
}

// `gate stats` is where the number is looked up on purpose, so it states both
// halves — how many are open and how long the oldest has been.
func TestRenderGateStatsCarriesTheOpenEscapeCount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if err := appendEscape(EscapeRecord{
		ID: "a", Kind: EscapeKind, Reason: "x",
		At: time.Now().UTC().Add(-5 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	out := RenderGateStats(GateStats(strings.NewReader(""), time.Time{}))
	if !strings.Contains(out, "open escapes: 1") || !strings.Contains(out, "oldest 5d") {
		t.Fatalf("stats must state the escape debt:\n%s", out)
	}
}
