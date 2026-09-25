package mask

import "testing"

// Rust has no single-quoted strings: a `'` is either a char literal that
// closes a few bytes later on the same line, or the sigil of a lifetime or a
// loop label, which never closes at all. Reading the sigil as an opening
// quote blanks everything up to the next apostrophe, possibly many lines
// later, and every check reading the masked view then passes over code it
// never saw.
func TestRustTokens_MasksCharLiteralsAndLeavesLifetimesAsCode(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"a plain char literal":            {`c == 'x'`, `c == ' '`},
		"an escaped newline":              {`c == '\n'`, `c == '  '`},
		"an escaped quote":                {`c == '\''`, `c == '  '`},
		"an escaped backslash":            {`c == '\\'`, `c == '  '`},
		"a unicode escape":                {`c == '\u{1F600}'`, `c == '         '`},
		"a hex escape":                    {`c == '\x7f'`, `c == '    '`},
		"a multi-byte char":               {"c == 'é'", "c == '  '"},
		"a four-byte char":                {"c == '😀'", "c == '    '"},
		"two chars back to back":          {`['a','b']`, `[' ',' ']`},
		"a double quote as a char":        {`c == '"' && f("a")`, `c == ' ' && f(" ")`},
		"an anonymous lifetime":           {`fn a(e: &Elements<'_>) {}`, `fn a(e: &Elements<'_>) {}`},
		"a named lifetime":                {`fn a<'a>(x: &'a T) {}`, `fn a<'a>(x: &'a T) {}`},
		"the static lifetime":             {`const S: &'static str = "hi";`, `const S: &'static str = "  ";`},
		"a loop label":                    {`'outer: loop { break 'outer; }`, `'outer: loop { break 'outer; }`},
		"a lifetime and a char on a line": {`fn f<'a>(c: char) -> bool { c == 'x' }`, `fn f<'a>(c: char) -> bool { c == ' ' }`},
		"a lifetime then a string":        {`fn f<'a>() -> &'a str { "don't" }`, `fn f<'a>() -> &'a str { "     " }`},
	}
	for name, c := range cases {
		if got := RustTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// The issue's own repro: a lifetime on one line must not swallow the code on
// the lines below it up to the next apostrophe in the file.
func TestRustTokens_LifetimeNeverReachesTheNextLine(t *testing.T) {
	src := "fn a(elements: &Elements<'_>) {}\n" +
		"fn b(mass: f32, sub_dt: f32) -> f32 {\n" +
		"    mass / (sub_dt * sub_dt)\n" +
		"}\n" +
		"const C: char = 'z';\n"
	want := "fn a(elements: &Elements<'_>) {}\n" +
		"fn b(mass: f32, sub_dt: f32) -> f32 {\n" +
		"    mass / (sub_dt * sub_dt)\n" +
		"}\n" +
		"const C: char = ' ';\n"
	if got := RustTokens(src, true, false); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A char literal never spans a line: a quote closing the line and one opening
// the next are two separate things, and so is a backslash at the end of a
// line.
func TestRustTokens_QuoteNeverClosesOnTheNextLine(t *testing.T) {
	cases := map[string]string{
		"a quote at a line end and one at the next line start": "a'\n'b\nx\n",
		"a backslash escaping the newline":                     "a'\\\nb'\n",
		"a lone quote at the end of the input":                 "a'",
		"a char with no closing quote on its line":             "a'x\ny'\n",
		"an escape with no closing quote on its line":          "a'\\bc\nd'\n",
	}
	for name, src := range cases {
		if got := RustTokens(src, true, false); got != src {
			t.Errorf("%s: got %q, want it untouched", name, got)
		}
	}
}

// Comments are still recognised, so a lifetime next to a comment keeps the
// comment blankable and an apostrophe inside the comment opens nothing.
func TestRustTokens_CommentsStillBlank(t *testing.T) {
	src := "fn f<'a>() {} // it's fine\nlet x = 'y';\n"
	want := "fn f<'a>() {}             \nlet x = ' ';\n"
	if got := RustTokens(src, true, true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Go runes are char literals in the same shape and keep masking through the
// language-neutral lexer, which is left exactly as it was.
func TestTokens_GoRunesStillMask(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"a rune":            {`r == 'a'`, `r == ' '`},
		"an escaped quote":  {`r == '\''`, `r == '  '`},
		"a JS string":       {`f('abc def')`, `f('       ')`},
		"a shell multiline": {"echo 'a\nb'", "echo ' \n '"},
	}
	for name, c := range cases {
		if got := Tokens(c.src, true, false, false); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}
