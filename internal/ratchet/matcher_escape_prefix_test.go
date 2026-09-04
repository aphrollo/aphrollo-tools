package ratchet

import (
	"regexp"
	"testing"
)

// A law scoped over several file kinds cannot have ONE comment prefix. The
// escape window resolved its prefix from the law (defaulting to `//`), so a
// law scanning .rs, .md, .toml and .txt tested every one of them against Rust
// syntax. `transient_doc_reference` is exactly that law, and the consequence
// was that a `# sdd-ok:` comment — a real TOML comment, sitting directly
// above the hit, the shape the law's own message asks for — did not suppress
// anything, and every commit in the consuming repo was rejected.
func TestEscape_InATomlCommentAboveTheHit_IsHonoured(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`docs/sdd`), Key: KeyLineContent})
	l.Escape = "sdd-ok:"
	l.EscapeLines = 2
	src := "# sdd-ok: this line names the tree, it is not a citation into it\nsdd-dir = \"docs/sdd\"\n"
	if hits := l.HitsIn("Cargo.toml", src); len(hits) != 0 {
		t.Errorf("hits = %+v, want none — a TOML comment carrying the escape sits directly above the hit", hits)
	}
}

// Markdown has no comment syntax at all, so "the escape must open a real
// comment" is unsatisfiable there BY CONSTRUCTION: no edit to the line could
// ever have passed. A prose file's escape therefore counts inline, which is
// the only reading under which the law's own escape is usable in a .md file.
func TestEscape_InlineInMarkdownProse_IsHonoured(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`docs/sdd`), Key: KeyLineContent})
	l.Escape = "sdd-ok:"
	src := "- `docs/sdd/` (sdd-ok: this line names the tree, it is not a citation into it) holds specs\n"
	if hits := l.HitsIn("CLAUDE.md", src); len(hits) != 0 {
		t.Errorf("hits = %+v, want none — markdown has no comment syntax, so the escape counts inline", hits)
	}
}

// ...and the protection that motivated requiring a real comment survives
// where a comment syntax actually exists: in Rust the escape inside a string
// literal is code, and must not silence the hit.
func TestEscape_InAStringLiteralInRust_StillDoesNotSuppress(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`docs/sdd`), Key: KeyLineContent})
	l.Escape = "sdd-ok:"
	src := "let s = \"docs/sdd sdd-ok: not a real escape\";\n"
	if hits := l.HitsIn("a.rs", src); len(hits) != 1 {
		t.Errorf("hits = %+v, want one — the escape text sits inside a string literal, not a comment", hits)
	}
}

// A law that names its own comment_prefix keeps it, so a law deliberately
// scoped to one language is unaffected by per-file resolution.
func TestEscape_ExplicitLawCommentPrefixStillWins(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`docs/sdd`), Key: KeyLineContent})
	l.Escape = "sdd-ok:"
	l.CommentPrefix = ";;"
	l.EscapeLines = 2
	src := ";; sdd-ok: a lisp comment, because this law said so\nrefers to docs/sdd here\n"
	if hits := l.HitsIn("notes.txt", src); len(hits) != 0 {
		t.Errorf("hits = %+v, want none — the law names its own prefix and it must win", hits)
	}
}
