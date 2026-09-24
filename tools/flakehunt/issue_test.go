package main

import (
	"strings"
	"testing"
)

func exampleFailure() Failure {
	return Failure{
		Test:    "TestFlaky",
		Package: "internal/tdd/lock",
		Seed:    "1790255955024453711",
		Excerpt: "--- FAIL: TestFlaky (0.00s)\n    lock_test.go:15: boom\n",
	}
}

func TestTitleFor_NamesTheTestAndItsPackage(t *testing.T) {
	got := TitleFor(exampleFailure())
	want := "flaky: TestFlaky (internal/tdd/lock)"
	if got != want {
		t.Fatalf("TitleFor = %q, want %q", got, want)
	}
}

func TestReproCmd_CarriesRaceShuffleSeedAndCount(t *testing.T) {
	got := ReproCmd(exampleFailure())
	want := "go test ./internal/tdd/lock -race -run '^TestFlaky$' -count=50 -shuffle=1790255955024453711"
	if got != want {
		t.Fatalf("ReproCmd = %q, want %q", got, want)
	}
}

func TestBodyFor_CarriesSeedReproExcerptAndRunURL(t *testing.T) {
	body := BodyFor(exampleFailure(), "https://github.com/aphrollo/aphrollo-tools/actions/runs/1")
	for _, want := range []string{
		"1790255955024453711",
		"go test ./internal/tdd/lock -race -run '^TestFlaky$'",
		"boom",
		"https://github.com/aphrollo/aphrollo-tools/actions/runs/1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestSearchArgv_SearchesOpenIssuesByExactTitle(t *testing.T) {
	args := searchArgv(`flaky: TestFlaky (internal/tdd/lock)`)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "issue list") {
		t.Fatalf("searchArgv does not invoke gh issue list: %v", args)
	}
	if !strings.Contains(joined, "--state open") {
		t.Fatalf("searchArgv does not scope to open issues: %v", args)
	}
	if !strings.Contains(joined, `"flaky: TestFlaky (internal/tdd/lock)" in:title`) {
		t.Fatalf("searchArgv does not search by exact quoted title: %v", args)
	}
}

func TestCommentArgv_CommentsOnTheGivenIssueNumber(t *testing.T) {
	args := commentArgv(704, "recurred")
	want := []string{"issue", "comment", "704", "--body", "recurred"}
	if len(args) != len(want) {
		t.Fatalf("commentArgv = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("commentArgv[%d] = %q, want %q (full: %v)", i, args[i], want[i], args)
		}
	}
}

func TestFindExactOpenIssue_MatchesTitleExactlyNotBySubstring(t *testing.T) {
	payload := `[
		{"number": 12, "title": "flaky: TestOther (internal/tdd/lock)"},
		{"number": 7, "title": "flaky: TestFlaky (internal/tdd/lock)"}
	]`
	number, found, err := findExactOpenIssue(payload, "flaky: TestFlaky (internal/tdd/lock)")
	if err != nil {
		t.Fatalf("findExactOpenIssue: %v", err)
	}
	if !found || number != 7 {
		t.Fatalf("findExactOpenIssue = (%d, %v), want (7, true)", number, found)
	}
}

func TestFindExactOpenIssue_NoMatchReportsNotFound(t *testing.T) {
	number, found, err := findExactOpenIssue(`[]`, "flaky: TestFlaky (internal/tdd/lock)")
	if err != nil {
		t.Fatalf("findExactOpenIssue: %v", err)
	}
	if found || number != 0 {
		t.Fatalf("findExactOpenIssue = (%d, %v), want (0, false)", number, found)
	}
}
