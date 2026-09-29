package mask

import "testing"

// Go keeps the language-neutral lexer byte for byte: runes and both string
// forms blank, `//` and `/* */` are comments, and a raw string may span lines
// with a `//` inside it that opens nothing.
func TestTokens_GoMasksStringsAndComments(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an interpreted string with an escaped quote": {`s := "a \"b\" c"`, `s := "         "`},
		"a raw string spanning lines":                 {"s := `a\n// b`\nx", "s := ` \n    `\nx"},
		"a line comment with an apostrophe":           {"x := 1 // it's\ny := 'z'", "x := 1        \ny := ' '"},
		"a block comment spanning lines":              {"a /* it's\n\"q */ b", "a        \n      b"},
		"a hash is code":                              {`x := "#" + y # 'z'`, `x := " " + y # ' '`},
	}
	for name, c := range cases {
		if got := Tokens(c.src, true, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// JS and TS keep the language-neutral lexer byte for byte: a `#` is a
// private-field sigil, never a comment, and a template literal is raw.
func TestTokens_JSMasksStringsAndKeepsPrivateFields(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"a private field before a string":   {`this.#x = 'a b'`, `this.#x = '   '`},
		"a template literal spanning lines": {"f(`a\n'b`)", "f(` \n  `)"},
		"a regex-looking line comment":      {"x = 1 // don't\ny = \"q\"", "x = 1         \ny = \" \""},
	}
	for name, c := range cases {
		if got := Tokens(c.src, true, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}
