package ratchet

import (
	"regexp"
	"testing"
)

// code_only strips a comment, and a C-style block comment is one: a one-line
// JSDoc, and every line of a multi-line one, is prose to a law about code. A
// law that matched the English word "any" in such a line rejected commits with
// no `any` type anywhere.
func TestCodeOnly_IgnoresAMatchInABlockComment(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\bany\b`), Key: KeyLineContent})
	l.CodeOnly = true
	cases := map[string]struct {
		src  string
		want []int
	}{
		"one-line doc block": {"/** whether any points lie inside */\nexport const a = 1;\n", nil},
		"every line of a multi-line block": {
			"/**\n * any points whose first two numbers are x and y\n * or any other\n */\nexport const a = 1;\n", nil},
		"code after a block comment on the same line":  {"/* note */ const a: any = 1;\n", []int{1}},
		"code before a block comment on the same line": {"const a: any = 1; /* any */\n", []int{1}},
		"code right after a multi-line block closes":   {"/*\n any\n*/ let b: any;\n", []int{3}},
		"a block opener inside a string is code":       {"const s = \"/* not a comment\"; let a: any;\n/* real */\n", []int{1}},
		"a slash-star inside a line comment is prose":  {"// see /* any\nlet a: any;\n", []int{2}},
		"a line comment still ends at the line":        {"let a: any; // any\n", []int{1}},
	}
	for name, c := range cases {
		var got []int
		for _, h := range l.HitsIn("a.ts", c.src) {
			got = append(got, h.Line)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: hits on lines %v, want %v", name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: hits on lines %v, want %v", name, got, c.want)
			}
		}
	}
}

// The block-comment view is the file's own language's: a Rust lifetime's
// apostrophe must not open a quote that hides a comment after it, so the
// comment is blanked and the code is not.
func TestCodeOnly_BlockCommentsReadARustLifetimeAsCode(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\bTODO\b`), Key: KeyLineContent})
	l.CodeOnly = true
	src := "fn f(x: &'a str) {} /* TODO in prose */\nfn g() { let TODO = 1; }\n"
	hits := l.HitsIn("a.rs", src)
	if len(hits) != 1 || hits[0].Line != 2 {
		t.Fatalf("want one hit on line 2, got %+v", keys(hits))
	}
}

// A line that itself ends in a carriage return (the file had "\r\r\n") is
// rejoined with "\n" for the file-level lexer, which makes that "\r\n"; reading
// the lexed text back folded it, so the lexed line came back one byte shorter
// than the line the per-line cut kept, and slicing it panicked.
func TestCodeOnly_ALineEndingInALoneCarriageReturnDoesNotPanic(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\bany\b`), Key: KeyLineContent})
	l.CodeOnly = true
	hits := l.HitsIn("a.ts", "let a: any;\r\r\n/* x */ let b: any;\r\r\n")
	if len(hits) != 2 || hits[0].Line != 1 || hits[1].Line != 2 {
		t.Fatalf("want hits on lines 1 and 2, got %+v", keys(hits))
	}
}
