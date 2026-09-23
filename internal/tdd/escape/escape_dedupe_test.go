package escape

import (
	"io"
	"strings"
	"testing"
	"time"
)

// The reproduction this file is written from: `override:test-sleep` was filed
// as #698, fixed by the PR that narrowed the check, and the issue closed on
// that merge. The very next `gate escape sync` opened #701, carrying the same
// fingerprint, from the same nine pre-fix `pretooluse-denied:test-sleep`
// lines — which sit in the rolling seven-day gate.log for another week after
// the fix that answered them.
//
// Closing the issue is what let the next sync open another, so the fix is one
// rule: a CLOSED issue carrying the fingerprint suppresses too, and only
// evidence dated AFTER its close lifts that. The second half is the half
// worth guarding hardest — a suppression that never lifts is worse than the
// duplicate it replaced.

// closedIssueStub answers `issue list` with one CLOSED issue carrying
// fingerprint, and lets `issue create` succeed so a test can tell the two
// outcomes apart by whether the create was reached at all.
func closedIssueStub(t *testing.T, fingerprint string, closedAt time.Time) (argvLog string) {
	t.Helper()
	return stubGhScript(t, map[string]string{
		"issue list": `[{"number":698,"state":"CLOSED","closedAt":"` + closedAt.Format(time.RFC3339) +
			`","title":"false-positive: something else entirely","body":"` + issueFingerprintKey + ` ` + fingerprint + `"}]`,
		"issue create": "https://github.com/o/r/issues/701",
	})
}

// overrideSyncFixture is the sync as `escape sync` runs it, minus the file
// read: the log lines a real gate wrote, scanned into candidates by the
// production scanner, so the fingerprint under test is the one the recorder
// would actually compute rather than one a test invented.
func overrideSyncFixture(t *testing.T, at time.Time) (repo string, candidates []OverrideCandidate) {
	t.Helper()
	repo = makeGitHubRepo(t)
	log := gateLines(at,
		"preedit root internal/tdd/cargo_pathmod_test.go pretooluse-denied:test-sleep",
		"preedit root internal/tdd/cargo_pathmod_test.go smell-escape:test-sleep",
	)
	candidates = OverrideCandidates(strings.NewReader(log), time.Now().UTC())
	if len(candidates) != 1 {
		t.Fatalf("the fixture log must yield exactly one candidate, got %d: %+v", len(candidates), candidates)
	}
	return repo, candidates
}

// The defect itself: every log line that makes this candidate is older than
// the close of the issue that already answered it, so the sync has nothing
// new to say and must say nothing.
func TestRecordOverrideCandidates_OpensNoSecondIssueWhenTheEvidencePredatesAClose(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	evidenceAt := time.Now().UTC().Add(-4 * 24 * time.Hour)
	repo, cands := overrideSyncFixture(t, evidenceAt)
	argv := closedIssueStub(t, overrideFingerprint(repo, cands[0]), evidenceAt.Add(24*time.Hour))

	if n := RecordOverrideCandidates(repo, cands, io.Discard); n != 0 {
		t.Fatalf("opened %d issues for a candidate a merged fix already closed, want 0", n)
	}
	if strings.Contains(ghArgv(t, argv), "issue create") {
		t.Errorf("no duplicate must be created while every log line predates the close:\n%s", ghArgv(t, argv))
	}
	if recs := readEscapes(t); len(recs) != 0 {
		t.Errorf("nothing must be recorded locally either: %+v", recs)
	}
}

// The half that must not regress: the check was narrowed, the issue closed,
// and it refused correct work AGAIN afterwards. That is a fix that did not
// hold, which is the loudest signal this loop produces — it gets filed.
func TestRecordOverrideCandidates_FilesAgainWhenTheOverrideRecursAfterTheClose(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	evidenceAt := time.Now().UTC().Add(-2 * time.Hour)
	repo, cands := overrideSyncFixture(t, evidenceAt)
	argv := closedIssueStub(t, overrideFingerprint(repo, cands[0]), evidenceAt.Add(-24*time.Hour))

	if n := RecordOverrideCandidates(repo, cands, io.Discard); n != 1 {
		t.Fatalf("opened %d issues for an override that recurred after the close, want 1", n)
	}
	if !strings.Contains(ghArgv(t, argv), "issue create") {
		t.Errorf("a recurrence after the close must open its own issue:\n%s", ghArgv(t, argv))
	}
}

// An OPEN issue carrying the fingerprint is still the record, whatever its
// title says. The title guard alone missed this: it matches the stage name in
// a title, and the fingerprint is what identifies the miss.
func TestRecordOverrideCandidates_OpensNoSecondIssueWhileOneIsStillOpen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	evidenceAt := time.Now().UTC().Add(-2 * time.Hour)
	repo, cands := overrideSyncFixture(t, evidenceAt)
	argv := stubGhScript(t, map[string]string{
		"issue list": `[{"number":698,"state":"OPEN","closedAt":"","title":"false-positive: something else entirely","body":"` +
			issueFingerprintKey + ` ` + overrideFingerprint(repo, cands[0]) + `"}]`,
		"issue create": "https://github.com/o/r/issues/701",
	})

	if n := RecordOverrideCandidates(repo, cands, io.Discard); n != 0 {
		t.Fatalf("opened %d issues while one is open for the same fingerprint, want 0", n)
	}
	if strings.Contains(ghArgv(t, argv), "issue create") {
		t.Errorf("no second issue must be created:\n%s", ghArgv(t, argv))
	}
}

// The candidate's date is what the close is compared against, so it has to be
// the NEWEST sighting in the window, not the first one the scan met. A
// candidate stamped with its oldest line would stay suppressed through a
// recurrence that happened yesterday.
func TestOverrideCandidates_StampTheNewestSightingNotTheFirst(t *testing.T) {
	now := time.Now().UTC()
	first := now.Add(-5 * 24 * time.Hour)
	log := gateLines(first,
		"preedit root a_test.go pretooluse-denied:test-sleep",
		"preedit root a_test.go smell-escape:test-sleep",
	) + gateLines(now.Add(-time.Hour),
		"preedit root b_test.go pretooluse-denied:test-sleep",
		"preedit root b_test.go smell-escape:test-sleep",
	)

	got := OverrideCandidates(strings.NewReader(log), now)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	if !got[0].At.After(now.Add(-2 * time.Hour)) {
		t.Fatalf("candidate stamped %s, want the newest sighting near %s — an old stamp stays suppressed through a fresh recurrence",
			got[0].At.Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))
	}
}

// The guard reads the fingerprint off the issue BODY, so the issue this
// recorder opens has to carry the one the next sync will look for. If the two
// spellings drift the lookup matches nothing and every sync files a duplicate
// — the guard would be there and do nothing.
func TestRecordOverrideCandidates_FileTheIssueUnderTheFingerprintTheGuardLooksFor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo, cands := overrideSyncFixture(t, time.Now().UTC().Add(-time.Hour))
	argv := stubGhScript(t, map[string]string{
		"issue list":   `[]`,
		"issue create": "https://github.com/o/r/issues/701",
	})

	if n := RecordOverrideCandidates(repo, cands, io.Discard); n != 1 {
		t.Fatalf("opened %d issues for a candidate with no issue at all, want 1", n)
	}
	want := issueFingerprintKey + " " + overrideFingerprint(repo, cands[0])
	if !strings.Contains(ghArgv(t, argv), want) {
		t.Fatalf("the issue body must carry %q, or the next sync's lookup matches nothing:\n%s", want, ghArgv(t, argv))
	}
}

// A recorder that goes quiet is indistinguishable from one that is broken, so
// a suppression names the issue that made it.
func TestRecordOverrideCandidates_SayWhichIssueSuppressedTheCandidate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	evidenceAt := time.Now().UTC().Add(-4 * 24 * time.Hour)
	repo, cands := overrideSyncFixture(t, evidenceAt)
	closedIssueStub(t, overrideFingerprint(repo, cands[0]), evidenceAt.Add(24*time.Hour))

	var out strings.Builder
	RecordOverrideCandidates(repo, cands, &out)
	if !strings.Contains(out.String(), "#698") {
		t.Fatalf("the suppression must name the issue that answers the class, got %q", out.String())
	}
}

// The escape half of the loop shares the guard, and CI red is evidence dated
// NOW: a workflow failing this minute postdates any close, so a closed issue
// carrying the fingerprint must not silence it.
func TestRecordCIEscape_RecordsAgainWhenAClosedIssueCarriesTheFingerprint(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	commitWithGreenGateNote(t, repo, "a tree the local gate proved")
	fp := escapeFingerprint("ci:build", EscapeOptions{
		Repo: repo, Evidence: "--- FAIL: TestAlpha", Reason: "build failed on a tip the local gate passed green",
	})
	closedIssueStub(t, fp, time.Now().UTC().Add(-24*time.Hour))

	if _, recorded := RecordCIEscape(CIEscapeOptions{Repo: repo, Job: "build", Evidence: "--- FAIL: TestAlpha"}, io.Discard); !recorded {
		t.Fatal("a CI red today is evidence newer than yesterday's close — it must be recorded")
	}
}
