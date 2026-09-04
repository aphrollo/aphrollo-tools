package ratchet

import (
	"regexp"
	"testing"
)

// TestRegexAbsent_EscapeInsideAStringLiteralDoesNotSuppress proves the
// escape token must open a REAL trailing comment on the trigger's own
// line — a string literal that happens to contain the escape text is
// code, not a comment, and must not silence the hit.
func TestRegexAbsent_EscapeInsideAStringLiteralDoesNotSuppress(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.Escape = "// nan-safe:"
	src := `let a = x.clamp(0.0, 1.0); let s = "// nan-safe: not a real escape";` + "\n"
	if hits := l.HitsIn("a.rs", src); len(hits) != 1 {
		t.Fatalf("hits = %+v, want one — the escape text sits inside a string literal, not a comment", hits)
	}
}

// TestRegexAbsent_EscapeInExecutableCodeAboveDoesNotSuppress proves the
// same for the escape window above the trigger: a line of genuinely
// executable code (a println! call) that happens to contain the escape
// substring must not silence a hit two lines below it — only an actual
// comment line does.
func TestRegexAbsent_EscapeInExecutableCodeAboveDoesNotSuppress(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.Escape = "// nan-safe:"
	src := "println!(\"// nan-safe: reason\");\nlet a = x.clamp(0.0, 1.0);\n"
	if hits := l.HitsIn("a.rs", src); len(hits) != 1 {
		t.Fatalf("hits = %+v, want one — the line above is executable code, not a comment", hits)
	}
}
