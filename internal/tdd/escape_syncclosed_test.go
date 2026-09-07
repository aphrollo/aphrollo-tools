package tdd

import (
	"strings"
	"testing"
	"time"
)

// Every escape issue carries exactly ONE kind label — `escape` or
// `false-positive`, never both — and gh reads repeated --label flags as an
// AND. So the single `issue list --label escape --label false-positive` call
// this reconciliation used to make answered `[]` for the whole store, and
// `gate escape sync` printed "opened 0 issue(s)" and closed nothing while 39
// of the 39 aphrollo-tools issues the local records point at were already
// closed on GitHub. The union of one query per label is what actually
// answers the question the loop is asking.
func TestSyncClosedEscapes_ClosesARecordWhoseIssueCarriesOneKindLabel(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	for _, r := range []EscapeRecord{
		{ID: "a", Kind: EscapeKind, Reason: "clippy warning reached main",
			At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9},
		{ID: "b", Kind: FalsePositiveKind, Reason: "the gate refused a correct rename",
			At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/21", Number: 21},
	} {
		if err := appendEscape(r); err != nil {
			t.Fatal(err)
		}
	}
	stubGh(t, "")
	// What GitHub actually holds: issue 9 labelled escape, issue 21 labelled
	// false-positive, and — because the labels AND — nothing at all for a
	// query that asks for both at once.
	t.Setenv("GH_STUB_ISSUE_LIST_LABELS_ESCAPE", `[{"number":9,"state":"CLOSED"}]`)
	t.Setenv("GH_STUB_ISSUE_LIST_LABELS_FALSE_POSITIVE", `[{"number":21,"state":"CLOSED"}]`)
	t.Setenv("GH_STUB_ISSUE_LIST_LABELS_ESCAPE_FALSE_POSITIVE", `[]`)

	if n := syncClosedEscapes(repo); n != 2 {
		t.Fatalf("syncClosedEscapes = %d, want 2 — both issues are closed on GitHub, one under each kind label", n)
	}
	for _, r := range readEscapes(t) {
		if !r.Closed {
			t.Errorf("record %s (%s) is still open locally; its issue %s is closed on GitHub", r.ID, r.Kind, r.Issue)
		}
	}
}

// Issue numbers are per repository, and the store holds records opened
// against more than one — several aphrollo-tools records point at issues in
// another project entirely. Keyed on the number alone, a closed issue 9 in
// the repo being queried marks EVERY record numbered 9 closed, including one
// whose issue is still open somewhere else, and the debt disappears without
// anybody fixing it.
func TestSyncClosedEscapes_LeavesARecordFromAnotherRepositoryAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t) // origin is https://github.com/o/r.git
	for _, r := range []EscapeRecord{
		{ID: "here", Kind: EscapeKind, Reason: "this repo's escape",
			At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9},
		{ID: "elsewhere", Kind: EscapeKind, Reason: "another project's escape, same number",
			At: time.Now().UTC(), Issue: "https://github.com/other/project/issues/9", Number: 9},
	} {
		if err := appendEscape(r); err != nil {
			t.Fatal(err)
		}
	}
	stubGh(t, "")
	t.Setenv("GH_STUB_ISSUE_LIST_LABELS_ESCAPE", `[{"number":9,"state":"CLOSED"}]`)

	if n := syncClosedEscapes(repo); n != 1 {
		t.Fatalf("syncClosedEscapes = %d, want 1 — only o/r was asked about", n)
	}
	for _, r := range readEscapes(t) {
		switch {
		case r.ID == "here" && !r.Closed:
			t.Errorf("o/r issue 9 is closed on GitHub, so record %s must be closed locally", r.ID)
		case r.ID == "elsewhere" && r.Closed:
			t.Errorf("record %s points at %s, which was never queried — closing it invents a fix", r.ID, r.Issue)
		}
	}
}

// A record whose issue lives in the queried repo is reconciled by the URL,
// not by the remote's spelling: an SSH origin and an https issue link name
// the same repository.
func TestSyncClosedEscapes_MatchesAnSSHRemoteAgainstAnHTTPSIssueURL(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	gitDo(t, repo, "remote", "add", "origin", "git@github.com:o/r.git")
	if err := appendEscape(EscapeRecord{
		ID: "a", Kind: EscapeKind, Reason: "ssh remote, https issue url",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	stubGh(t, "")
	t.Setenv("GH_STUB_ISSUE_LIST_LABELS_ESCAPE", `[{"number":9,"state":"CLOSED"}]`)

	if n := syncClosedEscapes(repo); n != 1 {
		t.Fatalf("syncClosedEscapes = %d, want 1 — git@github.com:o/r.git is the same repo as github.com/o/r", n)
	}
}

// A query that fails reconciles nothing: gh saying it could not reach GitHub
// is not evidence that an issue was closed.
func TestSyncClosedEscapes_ClosesNothingWhenEveryQueryFails(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		ID: "a", Kind: EscapeKind, Reason: "offline",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	stubGh(t, "")
	t.Setenv("GH_STUB_ISSUE_LIST_FAIL", "could not connect to api.github.com")

	if n := syncClosedEscapes(repo); n != 0 {
		t.Fatalf("syncClosedEscapes = %d, want 0 — every query failed", n)
	}
	if recs := readEscapes(t); len(recs) != 1 || recs[0].Closed {
		t.Fatalf("records = %+v, want the record left open after a failed fetch", recs)
	}
}

// The two queries are the fix, so they are what the argv has to show: one
// --label per call, never the AND of both.
func TestSyncClosedEscapes_AsksAboutOneLabelPerCall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		ID: "a", Kind: EscapeKind, Reason: "argv",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	log := stubGh(t, `[]`)

	syncClosedEscapes(repo)

	argv := ghArgv(t, log)
	for _, want := range []string{
		"issue list --label " + EscapeKind + " ",
		"issue list --label " + FalsePositiveKind + " ",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("gh argv is %q, want a call matching %q", argv, want)
		}
	}
	if strings.Contains(argv, "--label "+EscapeKind+" --label "+FalsePositiveKind) {
		t.Errorf("gh argv is %q; two --label flags in one call AND together and answer nothing", argv)
	}
}
