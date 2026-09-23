package tdd

import (
	"io"
	"strings"
	"testing"
	"time"
)

// A waiver on a file no check ever refused is an author declaring an
// exception, not a check being talked past — and the pairing is the only
// thing that tells the two apart.
func TestAWaiverWithNoPrecedingDenialIsNotACandidate(t *testing.T) {
	now := time.Now().UTC()
	log := gateLines(now, "preedit root a_test.go smell-escape:test-sleep")
	if got := OverrideCandidates(strings.NewReader(log), now); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %+v", len(got), got)
	}
}

// Two mechanical rejections whose first line is the same boilerplate are two
// DIFFERENT misses when different tests failed. Fingerprinting the constant
// alone lets one record suppress every unrelated failure for a week, silently.
func TestTwoMechanicalRejectionsWithDifferentFailingTestsAreDifferentEscapes(t *testing.T) {
	first := EscapeOptions{Evidence: "TDD mechanical: tests failing — fix before committing.\nfailing: TestAlpha\n    --- FAIL: TestAlpha (0.01s)\n"}
	second := EscapeOptions{Evidence: "TDD mechanical: tests failing — fix before committing.\nfailing: TestBeta\n    --- FAIL: TestBeta (0.01s)\n"}
	if a, b := escapeFingerprint("merge", first), escapeFingerprint("merge", second); a == b {
		t.Fatalf("two different failing tests share fingerprint %s — one would silence the other for a week", a)
	}
}

// The same failing test twice IS one miss, and must stay one.
func TestTheSameFailingTestKeepsOneFingerprint(t *testing.T) {
	first := EscapeOptions{Evidence: "TDD mechanical: tests failing — fix before committing.\nfailing: TestAlpha\n    --- FAIL: TestAlpha (0.01s)\ncommand: go test ./a\n"}
	second := EscapeOptions{Evidence: "TDD mechanical: tests failing — fix before committing.\nfailing: TestAlpha\n    --- FAIL: TestAlpha (0.02s)\ncommand: go test ./b\n"}
	if a, b := escapeFingerprint("merge", first), escapeFingerprint("merge", second); a != b {
		t.Fatalf("the same failing test must be one class: %s vs %s", a, b)
	}
}

// Two repos failing the same way are two misses, not one. Without the repo in
// the fingerprint, the first repo to record `test` silences every other repo
// on the box for a week.
func TestTheSameDiagnosticInTwoReposAreDifferentEscapes(t *testing.T) {
	o := EscapeOptions{Evidence: "boom", Repo: "/a"}
	other := EscapeOptions{Evidence: "boom", Repo: "/b"}
	if a, b := escapeFingerprint("ci:pipeline", o), escapeFingerprint("ci:pipeline", other); a == b {
		t.Fatalf("two repos share fingerprint %s", a)
	}
}

// A suppressed duplicate that says nothing looks exactly like a recorder that
// is broken. One line, so the operator can tell the two apart.
func TestASuppressedDuplicateSaysSo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGh(t, "https://github.com/o/r/issues/3")
	o := EscapeOptions{Reason: "the merge gate refused a green lane", Repo: repo}

	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("the first sighting must be recorded")
	}
	var out strings.Builder
	if _, recorded := recordEscapeOnce("merge:premergecommit", o, &out); recorded {
		t.Fatal("the second must be suppressed")
	}
	if !strings.Contains(out.String(), "already open") {
		t.Fatalf("a suppressed duplicate must say so, got %q", out.String())
	}
}

// A record with no issue is evidence nobody can see. Letting it suppress the
// next sighting burns the whole class for a week over a gh that was briefly
// unreachable — so it does not suppress; it is RETRIED.
func TestARecordWhoseIssueNeverOpenedIsRetriedRatherThanSuppressing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGh(t, "https://github.com/o/r/issues/11")
	o := EscapeOptions{Reason: "the merge gate refused a green lane", Repo: repo}

	// First sighting while gh cannot answer: recorded locally, no issue.
	t.Setenv("GH_STUB_ISSUE_CREATE_FAIL", "gh: could not resolve host")
	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("the first sighting must be recorded even when gh fails")
	}
	if recs := readEscapes(t); len(recs) != 1 || recs[0].Issue != "" {
		t.Fatalf("want one unsynced record, got %+v", recs)
	}

	// Second sighting, gh back: the SAME record gets its issue, and no
	// second record is appended.
	t.Setenv("GH_STUB_ISSUE_CREATE_FAIL", "")
	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("an unsynced record must not suppress the next sighting")
	}
	recs := readEscapes(t)
	if len(recs) != 1 {
		t.Fatalf("want the one record retried, not a second appended: %+v", recs)
	}
	if recs[0].Issue != "https://github.com/o/r/issues/11" {
		t.Fatalf("the retry must attach the issue to the existing record: %+v", recs[0])
	}
}

// `escape sync` reporting "opened N issue(s)" from local records alone is a
// number that means nothing: with no gh, or no GitHub remote, nothing was
// opened. The demote scan already guards on both, and the override half must
// not be the one place the count lies.
func TestOverrideCandidatesOpenNothingWithoutAGitHubRemote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cands := []OverrideCandidate{{Stage: "override:override-off", Reason: "r", Evidence: "e"}}
	if n := RecordOverrideCandidates(t.TempDir(), cands, io.Discard); n != 0 {
		t.Fatalf("opened %d with no remote to open against, want 0", n)
	}
	if recs := readEscapes(t); len(recs) != 0 {
		t.Fatalf("nothing must be recorded either: %+v", recs)
	}
}

// A check re-flagged every week must not collect a new issue every week: the
// OPEN issue is the record, and the local dedupe cannot see one opened from
// another checkout or by another box.
func TestAnOverrideWithAnOpenIssueAlreadyOpensNoSecond(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{
		"issue list":   `[{"title":"false-positive: override:override-off went around the gate"}]`,
		"issue create": "https://github.com/o/r/issues/30",
	})
	cands := []OverrideCandidate{{Stage: "override:override-off", Reason: "r", Evidence: "e"}}

	if n := RecordOverrideCandidates(repo, cands, io.Discard); n != 0 {
		t.Fatalf("opened %d for a check that already has an open issue, want 0", n)
	}
	if strings.Contains(ghArgv(t, log), "issue create") {
		t.Errorf("no second issue must be created:\n%s", ghArgv(t, log))
	}
}

// The dedupe store lives in this box's gate-state, which an ephemeral CI
// runner does not have: every re-run would start from an empty log and open
// another issue for the same red. The issue itself has to carry the
// fingerprint, so the runner can ask GitHub what it cannot remember.
func TestTheEscapeIssueBodyCarriesItsFingerprint(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/12")

	r, recorded := recordEscapeOnce("ci:pipeline", EscapeOptions{
		Reason: "the suite went red in CI", Evidence: "--- FAIL: TestAlpha", Repo: repo,
	}, io.Discard)
	if !recorded {
		t.Fatal("the first sighting must be recorded")
	}
	if !strings.Contains(ghArgv(t, log), issueFingerprintKey+" "+r.Fingerprint) {
		t.Fatalf("the issue body must carry %s %s:\n%s", issueFingerprintKey, r.Fingerprint, ghArgv(t, log))
	}
}

// With no local record to go on, an open issue already carrying this
// fingerprint is the record — and one issue per CI re-run is exactly the
// noise the dedupe exists to prevent.
func TestCIEscapeWithAnOpenIssueCarryingItsFingerprintOpensNoSecond(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	gitDo(t, root, "remote", "add", "origin", "https://github.com/o/r.git")
	commitWithGreenGateNote(t, root, "Land it")

	o := CIEscapeOptions{Repo: root, Job: "pipeline", Evidence: "--- FAIL: TestAlpha"}
	fp := escapeFingerprint("ci:"+o.Job, EscapeOptions{Repo: o.Repo, Evidence: o.Evidence})
	log := stubGhScript(t, map[string]string{
		"issue list":   `[{"number":9,"body":"` + issueFingerprintKey + ` ` + fp + `"}]`,
		"issue create": "https://github.com/o/r/issues/99",
	})

	if _, recorded := RecordCIEscape(o, io.Discard); recorded {
		t.Fatal("an open issue already carrying this fingerprint is the record")
	}
	if strings.Contains(ghArgv(t, log), "issue create") {
		t.Errorf("no second issue must be created:\n%s", ghArgv(t, log))
	}
}

// A different red on the same job is a different miss, and an open issue for
// the first must not silence it.
func TestCIEscapeWithAnUnrelatedOpenIssueStillRecords(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	gitDo(t, root, "remote", "add", "origin", "https://github.com/o/r.git")
	commitWithGreenGateNote(t, root, "Land it")

	stubGhScript(t, map[string]string{
		"issue list":   `[{"number":9,"body":"` + issueFingerprintKey + ` 0000000000000000"}]`,
		"issue create": "https://github.com/o/r/issues/99",
	})
	o := CIEscapeOptions{Repo: root, Job: "pipeline", Evidence: "--- FAIL: TestAlpha"}
	if _, recorded := RecordCIEscape(o, io.Discard); !recorded {
		t.Fatal("an unrelated open issue must not suppress a new miss")
	}
}
