package ratchet

import (
	"sort"
	"testing"
)

// datedCommentLaw is the consuming repo's `dated_comment_py` law from #944,
// verbatim in every key that decides what the masked view is.
func datedCommentLaw(t *testing.T) Law {
	t.Helper()
	l, err := ParseLaw(`name = "dated_comment_py"
description = "a comment carries no date"
severity = "deny"
mask_strings = true
comment_prefix = "#"

[scope]
include = ["**/*.py"]

[matcher]
kind = "regex-absent"
pattern = "20\\d\\d-\\d\\d-\\d\\d"
key = "file:line-content-hash"
`, "dated_comment_py")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	return l
}

func hitKeys(hits []Hit) []string {
	keys := make([]string, 0, len(hits))
	for _, h := range hits {
		keys = append(keys, h.Key)
	}
	sort.Strings(keys)
	return keys
}

// #944: dated comments below an apostrophe in a comment were blanked as
// string content, so the baseline never counted them; an edit higher up that
// flipped the quote parity then surfaced every one as a regression. Each
// dated comment is seen before the edit, and the edit changes no key.
func TestLawMaskStrings_DatedPythonCommentsBelowCodeAreSeenAndStable(t *testing.T) {
	l := datedCommentLaw(t)
	tail := "def placeholder(col):\n" +
		"    # Postgres doesn't take ? here\n" +
		"    return f\"LOWER(COALESCE({col},''))\"\n" +
		"\n\n" +
		"def tally(rows):\n" +
		"    # 2026-05-30: exclude muted conversations from the dialog-state tally,\n" +
		"    total = sum(r.n for r in rows)\n" +
		"    # 2026-06-10 PERF: scope the TG branch here, NOT via an outer filter\n" +
		"    return total  # Scope echo (2026-06-04): the single-model filter this response\n"
	helper := "def like(p):\n" +
		"    # it's matched case-blind\n" +
		"    return f\"name LIKE {p}\"\n\n\n"
	before := l.HitsIn("unified_inbox.py", tail)
	if got, want := lineKeys(before), []string{"unified_inbox.py:7", "unified_inbox.py:9", "unified_inbox.py:10"}; !sameStrings(got, want) {
		t.Fatalf("before the edit: hits = %v, want %v", got, want)
	}
	after := l.HitsIn("unified_inbox.py", helper+tail)
	if b, a := hitKeys(before), hitKeys(after); !sameStrings(b, a) {
		t.Errorf("an edit above the comments changed their keys:\nbefore %v\n after %v", b, a)
	}
}

// #946 and #950: an apostrophe, or a quote that opens in one comment and
// closes in another lines below, sits inside a comment and opens no string. Deleting
// such a comment changes no other line's verdict, in any `#`-comment language
// the ratchet reads.
func TestLawMaskStrings_QuotesInHashCommentsOpenNoString(t *testing.T) {
	l := forbiddenWordLaw(t)
	body := "x = 1  # FORBIDDEN one\ny = 'q'\nz = 2  # FORBIDDEN two\n"
	comments := map[string][2]string{
		"an apostrophe":                  {"# the stage's output\n", ""},
		"a quote spanning comment lines": {"# the \"stage\n", "# ends\" here\n"},
	}
	for _, file := range []string{"script_agent.py", "run.sh", "config.toml"} {
		bare := hitKeys(l.HitsIn(file, body))
		if len(bare) != 2 {
			t.Fatalf("%s without a comment: hits = %v, want both FORBIDDEN lines", file, bare)
		}
		for name, comment := range comments {
			if got := hitKeys(l.HitsIn(file, comment[0]+body+comment[1])); !sameStrings(got, bare) {
				t.Errorf("%s under %s: hits = %v, want %v", file, name, got, bare)
			}
		}
	}
}
