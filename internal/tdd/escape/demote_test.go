package escape

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
	// Every check here was already refusing things before the oldest window
	// opened, so what the trend reads is the rise and not the check's age.
	for _, check := range []string{"pretooluse-denied:test-sleep", "pretooluse-denied:tautology", "smell-escape:disabled-test"} {
		log.WriteString(denyLine(now, 26*24*time.Hour, check))
	}
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
	want := DemoteCandidate{Repo: "/r", Check: "test-sleep"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("candidates = %v, want [%v]", got, want)
	}
}

// Denied and escaped are the two halves of one check's friction: a session
// that starts waiving a rule rather than tripping it is the same signal.
func TestDemoteCandidatesCountsDenialsAndEscapesTogether(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	log.WriteString(denyLine(now, 26*24*time.Hour, "pretooluse-denied:test-sleep"))
	log.WriteString(denyLine(now, 18*24*time.Hour, "pretooluse-denied:test-sleep"))
	log.WriteString(denyLine(now, 11*24*time.Hour, "pretooluse-denied:test-sleep"))
	log.WriteString(denyLine(now, 11*24*time.Hour, "smell-escape:test-sleep"))
	for range 3 {
		log.WriteString(denyLine(now, 3*24*time.Hour, "smell-escape:test-sleep"))
	}

	got := DemoteCandidates(strings.NewReader(log.String()), now)
	want := DemoteCandidate{Repo: "/r", Check: "test-sleep"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("candidates = %v, want [%v]", got, want)
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

	n := RecordDemoteCandidates(repo, []DemoteCandidate{{Repo: repo, Check: "test-sleep"}}, &strings.Builder{})
	if n != 1 {
		t.Fatalf("opened %d issues, want 1", n)
	}
	argv := ghArgv(t, log)
	if !strings.Contains(argv, FalsePositiveKind) || !strings.Contains(argv, "test-sleep") {
		t.Fatalf("the issue must be a labelled false-positive naming the check:\n%s", argv)
	}

	// Second run, with that issue now open and carrying the check's
	// fingerprint: nothing new. Dedupe goes by fingerprint, not by matching
	// words in a title — a title match is a guess, a fingerprint is the
	// identity.
	fp := demoteFingerprint(repo, "test-sleep")
	stubGhScript(t, map[string]string{
		"issue list":   `[{"number":5,"state":"OPEN","body":"` + issueFingerprintKey + ` ` + fp + `"}]`,
		"issue create": "https://github.com/o/r/issues/6",
	})
	if n := RecordDemoteCandidates(repo, []DemoteCandidate{{Repo: repo, Check: "test-sleep"}}, &strings.Builder{}); n != 0 {
		t.Fatalf("opened %d more issues; an open one is already the record", n)
	}
}

// The lookup that dedupes across syncs matches on the fingerprint IN THE
// BODY, so the body must actually carry a non-blank one. An empty
// `gate-fingerprint:` line can never match anything the lookup asks for, so
// every sync re-opens.
func TestRecordDemoteCandidates_IssueBodyCarriesAFingerprint(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{
		"issue list":   `[]`,
		"issue create": "https://github.com/o/r/issues/5",
	})

	if n := RecordDemoteCandidates(repo, []DemoteCandidate{{Repo: repo, Check: "test-sleep"}}, &strings.Builder{}); n != 1 {
		t.Fatalf("opened %d issues, want 1", n)
	}

	argv := ghArgv(t, log)
	i := strings.Index(argv, issueFingerprintKey)
	if i < 0 {
		t.Fatalf("issue body has no %s line at all:\n%s", issueFingerprintKey, argv)
	}
	after := strings.TrimLeft(argv[i+len(issueFingerprintKey):], " ")
	if after == "" || after[0] == '\n' {
		t.Fatalf("the fingerprint after %s is blank, so the lookup that dedupes across syncs can never match it:\n%s", issueFingerprintKey, argv)
	}
}

// A human closing the demote-candidate issue is a judgement the next sync
// must not undo. The two-week rise that flagged this run overlaps days
// before the close, so the evidence is not genuinely new: staying silent
// is correct.
func TestRecordDemoteCandidates_StaysClosedOnStaleEvidence(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	fp := demoteFingerprint(repo, "test-sleep")
	closedAt := time.Now().UTC().Add(-3 * 24 * time.Hour) // inside the 14-day rise window
	log := stubGhScript(t, map[string]string{
		"issue list":   `[{"number":7,"state":"CLOSED","closedAt":"` + closedAt.Format(time.RFC3339) + `","body":"` + issueFingerprintKey + ` ` + fp + `"}]`,
		"issue create": "https://github.com/o/r/issues/8",
	})

	if n := RecordDemoteCandidates(repo, []DemoteCandidate{{Repo: repo, Check: "test-sleep"}}, &strings.Builder{}); n != 0 {
		t.Fatalf("opened %d for a check a human already closed on evidence inside the rise window, want 0", n)
	}
	if strings.Contains(ghArgv(t, log), "issue create") {
		t.Errorf("no issue must be created while the evidence predates the close:\n%s", ghArgv(t, log))
	}
}

// A regression that resumes entirely after the close IS new evidence, and
// staying silent forever would be the opposite failure.
func TestRecordDemoteCandidates_ReopensOnEvidenceEntirelyAfterTheClose(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	fp := demoteFingerprint(repo, "test-sleep")
	closedAt := time.Now().UTC().Add(-30 * 24 * time.Hour) // well before the 14-day rise window
	log := stubGhScript(t, map[string]string{
		"issue list":   `[{"number":7,"state":"CLOSED","closedAt":"` + closedAt.Format(time.RFC3339) + `","body":"` + issueFingerprintKey + ` ` + fp + `"}]`,
		"issue create": "https://github.com/o/r/issues/9",
	})

	if n := RecordDemoteCandidates(repo, []DemoteCandidate{{Repo: repo, Check: "test-sleep"}}, &strings.Builder{}); n != 1 {
		t.Fatalf("opened %d for a regression that resumed entirely after the close, want 1", n)
	}
	if !strings.Contains(ghArgv(t, log), "issue create") {
		t.Errorf("a new issue must be opened once the rise is newer than the close:\n%s", ghArgv(t, log))
	}
}
