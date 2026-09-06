package tdd

import (
	"strings"
	"testing"
)

// One keyword per number is exactly what GitHub honours, so this must pass
// clean: no warning (both numbers are properly closed) and no error.
func TestClosingKeywordFindings_OneKeywordPerNumberPassesClean(t *testing.T) {
	warnings, errors := closingKeywordFindings("Closes #12, closes #34 -- both fixed.")
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(errors) != 0 {
		t.Errorf("errors = %v, want none", errors)
	}
}

// "Closes #A, #B" is the shape GitHub closes only #A of -- the whole reason
// this check exists (issue #463's "closed only #297" of a three-number list).
// It has one correct rewrite and no legitimate use, so it must be an ERROR,
// not a warning.
func TestClosingKeywordFindings_CommaListAfterOneKeywordErrors(t *testing.T) {
	warnings, errors := closingKeywordFindings("Closes #12, #34, #56 in one pass.")
	if len(errors) != 1 {
		t.Fatalf("errors = %v, want exactly one comma-list error", errors)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none -- the comma-list numbers are an ERROR, not also a bare-mention warning", warnings)
	}
}

// A bare "#123" is sometimes a genuine cross-reference ("related to #123"),
// so it must warn and must NOT fail the check.
func TestClosingKeywordFindings_BareMentionWarnsOnly(t *testing.T) {
	warnings, errors := closingKeywordFindings("See #123 for the background; unrelated to this fix.")
	if len(errors) != 0 {
		t.Errorf("errors = %v, want none -- a bare mention is never an error", errors)
	}
	if len(warnings) != 1 || warnings[0] != "#123" {
		t.Fatalf("warnings = %v, want [#123]", warnings)
	}
}

// A PR body naming no issue at all is the common case (a split, a refactor, a
// revert) and must produce neither a warning nor an error.
func TestClosingKeywordFindings_NoMentionPassesClean(t *testing.T) {
	warnings, errors := closingKeywordFindings("Refactor the widget loader for clarity, no behavior change.")
	if len(warnings) != 0 || len(errors) != 0 {
		t.Fatalf("warnings = %v, errors = %v, want both empty", warnings, errors)
	}
}

// CheckPRCloses is the caller-facing verdict: false only when a comma-list
// error is present, true when the body only carries warnings or nothing.
func TestCheckPRCloses_CommaListFailsTheCheck(t *testing.T) {
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"pr view": `{"body":"Closes #1, #2"}`})

	var out strings.Builder
	ok, err := CheckPRCloses(repo, "9", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("CheckPRCloses returned ok=true for a comma-list body:\n%s", out.String())
	}
}

func TestCheckPRCloses_BareMentionPassesTheCheck(t *testing.T) {
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"pr view": `{"body":"related to #1"}`})

	var out strings.Builder
	ok, err := CheckPRCloses(repo, "9", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("CheckPRCloses returned ok=false for a bare mention:\n%s", out.String())
	}
}
