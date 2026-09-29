package mask

import "testing"

// In Python a `#` outside a string opens a comment to the end of the line,
// and nothing inside it opens a string. Read as a quote, the apostrophe of
// "it's" in a comment blanks every line up to the next apostrophe in the
// file, and a check reading the masked view passes over code it never saw.
func TestPythonTokens_CommentsOpenNoStringAndStringsHideNoComment(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an apostrophe in a comment": {
			"x = 1  # it's\ny = 'ab'\n",
			"x = 1  # it's\ny = '  '\n",
		},
		"a quote spanning two comment lines": {
			"# the \"stage\n# done\" here\ns = \"q\"\n",
			"# the \"stage\n# done\" here\ns = \" \"\n",
		},
		"a hash inside strings": {
			`s = "a # b" + 'c'  # don't`,
			`s = "     " + ' '  # don't`,
		},
		"an escaped quote in a string": {
			`s = 'it\'s' # don't`,
			`s = '     ' # don't`,
		},
		"a backslash continuing a string onto the next line": {
			"s = 'a\\\nb' # it's\nx = 'k'",
			"s = '  \n ' # it's\nx = ' '",
		},
		"an unterminated string ends at its line": {
			"s = 'unterminated\n# it's\nx = 'k'",
			"s = '            \n# it's\nx = ' '",
		},
		"an empty string at the end of the input": {
			"x = ''",
			"x = ''",
		},
	}
	for name, c := range cases {
		if got := PythonTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// Triple-quoted strings span lines and hold quotes, apostrophes and `#`
// without ending; the string prefixes change nothing about where a literal
// starts or stops, and a raw string's backslash still keeps a quote in.
func TestPythonTokens_TripleQuotesAndPrefixes(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"a triple double quote spanning lines": {
			"d = \"\"\"it's\n# no \"q\"\n\"\"\"\nx = 'k'",
			"d = \"\"\"    \n        \n\"\"\"\nx = ' '",
		},
		"a triple single quote holding an apostrophe": {
			"'''it's'''\nx = 'k'",
			"'''    '''\nx = ' '",
		},
		"quotes short of three do not close a triple": {
			"\"\"\"a\"b\"\"c\"\"\"\nx",
			"\"\"\"      \"\"\"\nx",
		},
		"an empty string before code": {
			"x = '' + y  # it's\nz = 'k'",
			"x = '' + y  # it's\nz = ' '",
		},
		"the r, b, f, rb and Rb prefixes": {
			`p = r'\d#' + b"#" + f"{a}#" + rb'\'#' + Rb"""#"""  # it's`,
			`p = r'   ' + b" " + f"    " + rb'   ' + Rb""" """  # it's`,
		},
	}
	for name, c := range cases {
		if got := PythonTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// Blanking is selective exactly as it is for the other lexers: comments blank
// only when asked, strings only when asked.
func TestPythonTokens_BlanksOnlyWhatIsAskedFor(t *testing.T) {
	src := "x = 'a' # it's 'q'\ny = 2"
	cases := map[string]struct {
		strs, comments bool
		want           string
	}{
		"comments only": {false, true, "x = 'a'           \ny = 2"},
		"strings only":  {true, false, "x = ' ' # it's 'q'\ny = 2"},
		"both":          {true, true, "x = ' '           \ny = 2"},
	}
	for name, c := range cases {
		if got := PythonTokens(src, c.strs, c.comments); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// A shell `#` opens a comment only where a word starts: `$#`, `${#a}` and
// `a#b` are code. A single-quoted string is raw and may span lines; an
// escaped quote outside a string is a literal character and opens nothing.
func TestShellTokens_CommentsAtWordStartAndRawSingleQuotes(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an apostrophe in a comment":   {"echo hi # it's\nx='a b'\n", "echo hi # it's\nx='   '\n"},
		"a comment at the input start": {"# it's\nx='q'", "# it's\nx=' '"},
		"a comment after a semicolon":  {"a;# it's\nb='q'", "a;# it's\nb=' '"},
		"a comment after a tab":        {"a\t# it's\nb='q'", "a\t# it's\nb=' '"},
		"hash sigils are code":         {"n=$# ; m=${#a} ; echo a#b 'q' # it's", "n=$# ; m=${#a} ; echo a#b ' ' # it's"},
		"a single quote is raw":        {`echo 'a\' 'b'`, `echo '  ' ' '`},
		"a double quote escapes":       {`echo "a\"b" 'c'`, `echo "    " ' '`},
		"an escaped quote in code":     {`echo don\'t 'q'`, `echo don\'t ' '`},
		"a single quote spanning lines": {
			"echo 'a\n# b'\nx='q'", "echo ' \n   '\nx=' '",
		},
	}
	for name, c := range cases {
		if got := ShellTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// TOML: a `#` outside a string is a comment wherever it stands, a literal
// '…' string is raw, a basic "…" string escapes, and both come in a
// triple-quoted multi-line form.
func TestTOMLTokens_CommentsAndTheFourStringForms(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an apostrophe in a comment":    {"# it's a key\nk = 'v'", "# it's a key\nk = ' '"},
		"a comment right after a value": {"k = 1#it's\nv = 'q'", "k = 1#it's\nv = ' '"},
		"a literal string is raw":       {"p = 'C:\\' # it's\nq = \"x\"", "p = '   ' # it's\nq = \" \""},
		"a basic string escapes":        {`s = "a\"#" # it's`, `s = "    " # it's`},
		"a multi-line literal string": {
			"m = '''\nit's\n'''\nk = 'v'", "m = '''\n    \n'''\nk = ' '",
		},
		"a multi-line basic string": {
			"m = \"\"\"a # b\n\"\"\"\nk = 'v'", "m = \"\"\"     \n\"\"\"\nk = ' '",
		},
	}
	for name, c := range cases {
		if got := TOMLTokens(c.src, true, false); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}
