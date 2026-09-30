package mask

import "testing"

// Ruby reads `#` as a comment outside a string, so an apostrophe in one opens
// no string and cannot blank the lines below it.
func TestRubyTokens_CommentsOpenNoString(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an apostrophe in a comment": {"x = 1 # it's\ny = 'ab'\n", "x = 1 # it's\ny = '  '\n"},
		"a hash inside a string":     {`s = "a # b" # don't`, `s = "     " # don't`},
		"an escaped quote":           {`s = 'it\'s' # don't`, `s = '     ' # don't`},
		"an interpolation":           {`s = "a #{b}" # don't`, `s = "      " # don't`},
		"an unterminated string ends at its line": {
			"s = 'open\n# it's\nx = 'k'", "s = '    \n# it's\nx = ' '",
		},
	}
	for name, c := range cases {
		if got := RubyTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// YAML opens a comment at a `#` that follows whitespace, and a quote opens a
// string only where a scalar starts: the apostrophe of a plain scalar's
// "don't" is text, and a single-quoted string holds `”` as one literal quote.
func TestYAMLTokens_CommentsAndScalarStarts(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an apostrophe in a comment": {"a: 1 # it's\nb: 'xy'\n", "a: 1 # it's\nb: '  '\n"},
		"a comment on its own line":  {"# it's\nb: 'xy'\n", "# it's\nb: '  '\n"},
		"an apostrophe in a plain scalar": {
			"msg: don't stop\nb: \"xy\"\n", "msg: don't stop\nb: \"  \"\n",
		},
		"a hash with no space before it is text": {
			"u: http://x/#it's\nb: 'xy'\n", "u: http://x/#it's\nb: '  '\n",
		},
		"a quote after the input's first byte": {"x'y' 'z'", "x'y' 'z'"},
		"a quote after a sequence dash":        {"- 'a b'\n", "- '   '\n"},
		"a quote after a flow opener":          {"k: ['a', \"b\"]\n", "k: [' ', \" \"]\n"},
		"a doubled quote stays inside":         {"k: 'it''s' # don't\nj: 'q'\n", "k: '     ' # don't\nj: ' '\n"},
		"a quote ending the input":             {"k: 'a'", "k: ' '"},
		"a doubled quote ending the input":     {"k: 'a''", "k: '   "},
		"a double quote escapes":               {`k: "a\"b" # don't`, `k: "    " # don't`},
		"an unterminated string ends at its line": {
			"k: 'open\n# it's\nj: 'q'\n", "k: '    \n# it's\nj: ' '\n",
		},
	}
	for name, c := range cases {
		if got := YAMLTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}
