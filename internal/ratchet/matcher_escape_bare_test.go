package ratchet

import (
	"regexp"
	"testing"
)

// TestRegexAbsentEscape_BareTokenWithNoReasonDoesNotSuppress proves an
// escape comment holding only the token — no text after it — does not
// exempt a hit, whether on the trigger's own line or on the line above.
// remedyFor tells the author to write `escape: <token> <why>`; a comment
// that stops at the token carries no reviewed reason, so it must not read
// as an escape at all.
func TestRegexAbsentEscape_BareTokenWithNoReasonDoesNotSuppress(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.Escape = "// nan-safe:"
	cases := map[string]struct {
		src  string
		want int
	}{
		"bare on trigger line":      {"let a = x.clamp(0.0, 1.0); // nan-safe:\n", 1},
		"bare with trailing spaces": {"let a = x.clamp(0.0, 1.0); // nan-safe:   \n", 1},
		"bare one line above":       {"// nan-safe:\nlet a = x.clamp(0.0, 1.0);\n", 1},
		"reasoned still suppresses": {"let a = x.clamp(0.0, 1.0); // nan-safe: literal\n", 0},
	}
	for name, c := range cases {
		if got := len(l.HitsIn("a.rs", c.src)); got != c.want {
			t.Errorf("%s: %d hits, want %d", name, got, c.want)
		}
	}
}

// TestEscape_BareTokenInProseDoesNotSuppress is the prose-file twin: a
// markdown line carrying the token with nothing after it must not read as
// an escape either, even though a prose file has no comment syntax to open.
func TestEscape_BareTokenInProseDoesNotSuppress(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`docs/sdd`), Key: KeyLineContent})
	l.Escape = "sdd-ok:"
	src := "the tree lives at docs/sdd, cited here sdd-ok:\n"
	if hits := l.HitsIn("CLAUDE.md", src); len(hits) != 1 {
		t.Errorf("hits = %+v, want one — the token carries no reason", hits)
	}
}
