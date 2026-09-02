package tdd

import (
	"strings"
	"testing"
	"time"
)

// denyLine is one gate.log entry for a check refusing or waiving something,
// `age` before now.
func denyLine(now time.Time, age time.Duration, verdict string) string {
	return now.Add(-age).Format(time.RFC3339) + " preedit /r a_test.go " + verdict + " 0.0s\n"
}

// A check that refuses more every week is either finding a real regression in
// how people work or refusing correct work — and the second is far more
// common. Two consecutive rising weeks is the signal to look, stated by name
// so nobody has to read the log to find it.
func TestDemoteCandidatesNamesAChecksRisingTwoWeeksRunning(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	// rising: 1 three weeks ago, 2 two weeks ago, 3 last week.
	log.WriteString(denyLine(now, 18*24*time.Hour, "pretooluse-denied:test-sleep"))
	for range 2 {
		log.WriteString(denyLine(now, 11*24*time.Hour, "pretooluse-denied:test-sleep"))
	}
	for range 3 {
		log.WriteString(denyLine(now, 3*24*time.Hour, "pretooluse-denied:test-sleep"))
	}
	// flat: three every week, no trend.
	for _, age := range []time.Duration{18, 11, 3} {
		for range 3 {
			log.WriteString(denyLine(now, age*24*time.Hour, "pretooluse-denied:tautology"))
		}
	}
	// one rise only: last week is up, the week before was down.
	for range 5 {
		log.WriteString(denyLine(now, 18*24*time.Hour, "smell-escape:disabled-test"))
	}
	log.WriteString(denyLine(now, 11*24*time.Hour, "smell-escape:disabled-test"))
	for range 2 {
		log.WriteString(denyLine(now, 3*24*time.Hour, "smell-escape:disabled-test"))
	}

	got := DemoteCandidates(strings.NewReader(log.String()), now)
	if len(got) != 1 || got[0] != "test-sleep" {
		t.Fatalf("candidates = %v, want [test-sleep]", got)
	}
}

// Denied and escaped are the two halves of one check's friction: a session
// that starts waiving a rule rather than tripping it is the same signal.
func TestDemoteCandidatesCountsDenialsAndEscapesTogether(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	log.WriteString(denyLine(now, 18*24*time.Hour, "pretooluse-denied:test-sleep"))
	log.WriteString(denyLine(now, 11*24*time.Hour, "pretooluse-denied:test-sleep"))
	log.WriteString(denyLine(now, 11*24*time.Hour, "smell-escape:test-sleep"))
	for range 3 {
		log.WriteString(denyLine(now, 3*24*time.Hour, "smell-escape:test-sleep"))
	}

	got := DemoteCandidates(strings.NewReader(log.String()), now)
	if len(got) != 1 || got[0] != "test-sleep" {
		t.Fatalf("candidates = %v, want [test-sleep]", got)
	}
}

// An override is a session flipping the whole gate off, not a check refusing
// anything, so it is not a check that can be demoted.
func TestDemoteCandidatesIgnoresOverrides(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	log.WriteString(denyLine(now, 18*24*time.Hour, "override-off"))
	for range 2 {
		log.WriteString(denyLine(now, 11*24*time.Hour, "override-off"))
	}
	for range 3 {
		log.WriteString(denyLine(now, 3*24*time.Hour, "override-off"))
	}
	if got := DemoteCandidates(strings.NewReader(log.String()), now); len(got) != 0 {
		t.Fatalf("candidates = %v, want none", got)
	}
}

// A candidate becomes a false-positive record like any other, so it lands in
// the same loop — and opening a second issue for the same check every week is
// how a signal turns into noise.
func TestRecordDemoteCandidatesOpensOneIssueAndThenStops(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{
		"issue list":   `[]`,
		"issue create": "https://github.com/o/r/issues/5",
	})

	n := RecordDemoteCandidates(repo, []string{"test-sleep"}, &strings.Builder{})
	if n != 1 {
		t.Fatalf("opened %d issues, want 1", n)
	}
	argv := ghArgv(t, log)
	if !strings.Contains(argv, FalsePositiveKind) || !strings.Contains(argv, "test-sleep") {
		t.Fatalf("the issue must be a labelled false-positive naming the check:\n%s", argv)
	}

	// Second run, with that issue now open: nothing new.
	stubGhScript(t, map[string]string{
		"issue list":   `[{"title":"false-positive: test-sleep denies more every week"}]`,
		"issue create": "https://github.com/o/r/issues/6",
	})
	if n := RecordDemoteCandidates(repo, []string{"test-sleep"}, &strings.Builder{}); n != 0 {
		t.Fatalf("opened %d more issues; an open one is already the record", n)
	}
}
