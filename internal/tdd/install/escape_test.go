package install

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func stubGh(t *testing.T, stdout string) (argvLog string) {
	t.Helper()
	return tddtest.StubGh(t, stdout)
}

func stubGhScript(t *testing.T, responses map[string]string) (argvLog string) {
	t.Helper()
	return tddtest.StubGhScript(t, responses)
}

func makeGitHubRepo(t *testing.T) string { t.Helper(); return tddtest.MakeGitHubRepo(t) }

func ghArgv(t *testing.T, log string) string { t.Helper(); return tddtest.GhArgv(t, log) }

func readEscapes(t *testing.T) []EscapeRecord {
	t.Helper()
	return tddtest.ReadEscapes[EscapeRecord](t, EscapeLogPath())
}

// A red after a local green is the only evidence the gate has that it is
// missing a check. Losing it means the same class escapes again next month,
// so it is written down before anything else can fail.
//
// ratchet: test_removed TestRecordEscapeWritesASchemaStampedRecord: renamed —
// EscapeRecord.Schema was written and never read by anything that branched
// on it (issue #511), so the field is gone and this test no longer checks it.
func TestRecordEscape_WritesARecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	if _, err := RecordEscape(EscapeOptions{Reason: "CI caught a clippy warning the gate did not"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	recs := readEscapes(t)
	if len(recs) != 1 {
		t.Fatalf("recorded %d escapes, want 1", len(recs))
	}
	r := recs[0]
	if r.Kind != EscapeKind {
		t.Errorf("kind = %q, want %q by default", r.Kind, EscapeKind)
	}
	if r.Reason != "CI caught a clippy warning the gate did not" || r.At.IsZero() {
		t.Errorf("record = %+v", r)
	}
}

func TestRecordEscapeRefusesAnUnknownKind(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, err := RecordEscape(EscapeOptions{Reason: "x", Kind: "whatever"}, io.Discard); err == nil {
		t.Fatal("an unknown kind must be refused, not silently recorded")
	}
}

// The issue is what makes the count go down: a line in a local file is a note
// nobody sees. The body is fixed so every escape states the same three
// things, and closes-by is what verify-closure later judges.
func TestRecordEscapeOpensALabelledIssue(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/42")

	r, err := RecordEscape(EscapeOptions{
		Reason:   "clippy warning reached main",
		Repo:     repo,
		FromCI:   "build (ubuntu-latest)",
		Evidence: "warning: unused variable `x`",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	argv := ghArgv(t, log)
	for _, want := range []string{"issue", "create", "--label", EscapeKind} {
		if !strings.Contains(argv, want) {
			t.Errorf("gh argv %q does not carry %q", argv, want)
		}
	}
	for _, want := range []string{"What got through", "Which stage should have caught it", "closes-by"} {
		if !strings.Contains(argv, want) {
			t.Errorf("the issue body must state %q; argv was %q", want, argv)
		}
	}
	if r.Issue != "https://github.com/o/r/issues/42" || r.Number != 42 {
		t.Errorf("the record must remember the issue it opened: %+v", r)
	}
}

// The recorder can already name the STAGE; naming the file that will close it
// is the same knowledge one step further, and an issue opened with the
// unfilled `closes-by: law | stage | demote check X` placeholder is one that
// verify-closure refuses every fix for until somebody hand-edits the body
// (issue #562). --closes-by fills the line at record time.
func TestEscapeIssueBody_StatesTheClosesByTheRecorderNamed(t *testing.T) {
	body := escapeIssueBody(EscapeRecord{
		Reason:   "a clippy warning reached main",
		ClosesBy: "internal/tdd/precommit_go.go",
	})
	if !strings.Contains(body, "closes-by: internal/tdd/precommit_go.go") {
		t.Fatalf("the body must state the closes-by it was given:\n%s", body)
	}
	if strings.Contains(body, "closes-by: law | stage") {
		t.Errorf("the placeholder must be replaced, not kept beside it:\n%s", body)
	}
}

// With nothing named, the line stays the prompt it always was — a blank means
// the reader still has to answer it.
func TestEscapeIssueBody_KeepsThePlaceholderWhenNothingIsNamed(t *testing.T) {
	body := escapeIssueBody(EscapeRecord{Reason: "a clippy warning reached main"})
	if !strings.Contains(body, "closes-by: law | stage") {
		t.Fatalf("an unnamed closes-by stays the prompt:\n%s", body)
	}
}

// A box with no gh, or a repo with no GitHub remote, still records: losing
// the evidence because a CLI is missing is the worst of both worlds.
func TestRecordEscapeStillRecordsWithoutGh(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	r, err := RecordEscape(EscapeOptions{Reason: "no gh here", Repo: t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r.Issue != "" {
		t.Errorf("no gh means no issue, got %q", r.Issue)
	}
	if len(readEscapes(t)) != 1 {
		t.Error("the record must survive an absent gh")
	}
}

// Sync is the catch-up path for everything recorded while gh was missing —
// and it must not re-open an issue that already exists.
func TestSyncEscapesOpensOnlyTheUnsyncedOnes(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	// Recorded with no repo to reach — the offline case sync exists for.
	if _, err := RecordEscape(EscapeOptions{Reason: "one"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordEscape(EscapeOptions{Reason: "two"}, io.Discard); err != nil {
		t.Fatal(err)
	}

	log := stubGh(t, "https://github.com/o/r/issues/7")
	var out strings.Builder
	n, err := SyncEscapes(repo, &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("synced %d, want 2", n)
	}
	if c := strings.Count(ghArgv(t, log), "issue create"); c != 2 {
		t.Fatalf("gh ran `issue create` %d times, want 2:\n%s", c, ghArgv(t, log))
	}

	again, err := SyncEscapes(repo, &out)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("a second sync opened %d more issues; a synced record is done", again)
	}
}

// A record whose issue closed on GitHub — a fix landed, the PR merged — must
// stop counting as open debt locally: nothing here polls GitHub on its own,
// so nothing ever learned an issue closed until sync reconciled it (issue
// #113).
func TestSyncEscapes_MarksARecordClosedWhenItsIssueClosedOnGitHub(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		ID: "x", Kind: EscapeKind, Reason: "already fixed",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	stubGhScript(t, map[string]string{"issue list": `[{"number":9,"state":"CLOSED"}]`})

	var out strings.Builder
	if _, err := SyncEscapes(repo, &out); err != nil {
		t.Fatal(err)
	}

	recs := readEscapes(t)
	if len(recs) != 1 || !recs[0].Closed {
		t.Fatalf("records = %+v, want the record marked closed", recs)
	}
	if open, _ := OpenEscapes(); open != 0 {
		t.Fatalf("open escapes = %d, want 0 once GitHub shows it closed", open)
	}
	if !strings.Contains(out.String(), "closed 1 locally (already closed on GitHub)") {
		t.Fatalf("output = %q, want the count of records just reconciled", out.String())
	}
}

// A record whose issue is STILL OPEN on GitHub is left alone — sync closes a
// loop, it does not guess one shut.
func TestSyncEscapes_LeavesAStillOpenIssueAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		ID: "x", Kind: EscapeKind, Reason: "still open",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	stubGhScript(t, map[string]string{"issue list": `[{"number":9,"state":"OPEN"}]`})

	var out strings.Builder
	if _, err := SyncEscapes(repo, &out); err != nil {
		t.Fatal(err)
	}

	recs := readEscapes(t)
	if len(recs) != 1 || recs[0].Closed {
		t.Fatalf("records = %+v, want the still-open record left alone", recs)
	}
	if strings.Contains(out.String(), "closed") {
		t.Fatalf("output = %q, nothing was reconciled — it must not claim otherwise", out.String())
	}
}

// A record that was never synced carries no GitHub issue number (0), so it
// has nothing on GitHub to be closed BY — it must not even cost a
// `gh issue list` call, let alone be reported reconciled.
func TestSyncClosedEscapes_SkipsRecordsWithNoIssueNumber(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		ID: "x", Kind: EscapeKind, Reason: "never synced",
		At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	log := stubGh(t, `[]`)

	if n := syncClosedEscapes(repo); n != 0 {
		t.Fatalf("syncClosedEscapes = %d, want 0 — the record was never synced", n)
	}
	if strings.Contains(ghArgv(t, log), "issue list") {
		t.Fatalf("gh was called (%s), want no call for a record with no issue number", ghArgv(t, log))
	}
}

// The count only goes down, so it has to be a number somebody sees.
func TestOpenEscapesCountsTheUnclosedAndTheOldest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	old := EscapeRecord{ID: "a", Kind: EscapeKind, Reason: "old", At: time.Now().UTC().Add(-30 * 24 * time.Hour)}
	recent := EscapeRecord{ID: "b", Kind: EscapeKind, Reason: "new", At: time.Now().UTC().Add(-2 * 24 * time.Hour)}
	closed := EscapeRecord{ID: "c", Kind: EscapeKind, Reason: "done", At: time.Now().UTC().Add(-90 * 24 * time.Hour), Closed: true}
	for _, r := range []EscapeRecord{old, recent, closed} {
		if err := appendEscape(r); err != nil {
			t.Fatal(err)
		}
	}
	n, oldest := OpenEscapes()
	if n != 2 {
		t.Fatalf("open = %d, want 2 (a closed record is paid off)", n)
	}
	if days := int(oldest.Hours() / 24); days != 30 {
		t.Fatalf("oldest = %d days, want 30", days)
	}
}

// The age column is the reason `escape list` exists at all: the oldest
// record is the one owed the most attention, and that only reads right if
// the day count is right.
func TestListEscapes_PrintsTheRecordsAgeInWholeDays(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	if err := appendEscape(EscapeRecord{
		ID: "a", Kind: EscapeKind, Reason: "ten days old",
		At: time.Now().UTC().Add(-10 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	ListEscapes(&out, false)
	if !strings.Contains(out.String(), "10d") {
		t.Fatalf("output = %q, want the age rendered as 10 whole days", out.String())
	}
}

// `escape list` prints open records by default — the ones somebody still
// owes a fix — and only names a closed one when asked for all of them.
func TestListEscapes_OpenByDefaultAllWithTheFlag(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	for _, r := range []EscapeRecord{
		{ID: "a", Kind: EscapeKind, Reason: "still open", At: time.Now().UTC()},
		{ID: "b", Kind: EscapeKind, Reason: "already fixed", At: time.Now().UTC(), Closed: true},
	} {
		if err := appendEscape(r); err != nil {
			t.Fatal(err)
		}
	}

	var openOnly strings.Builder
	ListEscapes(&openOnly, false)
	if !strings.Contains(openOnly.String(), "still open") {
		t.Fatalf("default list = %q, want the open record", openOnly.String())
	}
	if strings.Contains(openOnly.String(), "already fixed") {
		t.Fatalf("default list = %q, want the closed record left out", openOnly.String())
	}

	var all strings.Builder
	ListEscapes(&all, true)
	for _, want := range []string{"still open", "already fixed"} {
		if !strings.Contains(all.String(), want) {
			t.Fatalf("--all list = %q, want %q", all.String(), want)
		}
	}
}

// The managed CLAUDE.md block is where a session reads the rule, so the rule
// has to be in it.
func TestClaudeMDBlockStatesTheEscapeLoop(t *testing.T) {
	block := ClaudeMDBlock(BlockFlags{})
	for _, want := range []string{
		"**Escapes close the loop.**",
		"aphrollo gate escape record",
		"The count only goes down",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the managed block does not state %q", want)
		}
	}
}
