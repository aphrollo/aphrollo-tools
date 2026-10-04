package escape

import (
	"strings"
	"testing"
	"time"
)

// writeGateLog plants the stage lines of text, one gate line per row, as the
// events the gate writes for them under a state root of the test's own.
func writeGateLog(t *testing.T, text string) {
	t.Helper()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	for _, line := range strings.Split(text, "\n") {
		e, ok := parseGateLine(line)
		if !ok {
			continue
		}
		ev := Event{Kind: "gate", Root: e.Root, Stage: e.Stage, Verdict: e.Verdict, Secs: e.Secs, At: e.At.UTC().Format("2006-01-02T15:04:05.000Z07:00")}
		if isDenyVerdict(e.Verdict) || strings.HasPrefix(e.Verdict, "override-") {
			ev.Detail = map[string]string{"file": e.Cmd}
		} else {
			ev.Cmd = e.Cmd
		}
		AppendEvent(ev)
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

// An edit that waits in the run queue, and a queued run that starts, are not
// runs: counted as outcomes they would drag the green rate down for every edit
// that merely waited, and be a denominator for runs nobody made.
func TestWeeklyDigest_DoesNotCountQueueBookkeepingAsRuns(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	now := time.Now().UTC()
	stamp := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	writeGateLog(t, strings.Join([]string{
		stamp(time.Hour) + " postedit /r go test ./... green 1.0s",
		stamp(2*time.Hour) + " postedit /r go test ./b queue-waiting 0.0s",
		stamp(3*time.Hour) + " postedit /r go test ./b queue-started 0.0s",
		stamp(4*time.Hour) + " postedit /r go test ./c queued-dropped 0.0s",
		"",
	}, "\n"))

	line := weeklyDigest(now)

	if !strings.Contains(line, "green 50%") || !strings.Contains(line, "queued-skipped 50%") {
		t.Fatalf("digest %q: want one green of two runs (the waiting and started entries are no runs; the dropped one is a not-tested run)", line)
	}
}
