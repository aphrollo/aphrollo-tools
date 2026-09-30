package mask

import "bytes"

// The `#`-comment languages get lexers of their own rather than a flag on the
// language-neutral one. There a `#` outside a string opens a comment to the
// end of the line, and nothing inside that comment opens a string: read as a
// quote, the apostrophe of "it's" blanks every line up to the next apostrophe
// in the file, and a check reading the masked view passes over code it never
// saw. None of them has a `//` or `/* */` comment either — `//` is Python's
// floor division and `/*` a shell glob — so recognising those would only
// swallow real strings.

// hashDialect is what separates the `#`-comment languages from one another.
type hashDialect struct {
	// triple: `'''…'''` and `"""…"""` are strings that span lines.
	triple bool
	// lineStrings: a `'…'` or `"…"` string ends at an unescaped newline.
	lineStrings bool
	// rawSingle: a `'…'` string has no escapes, so a backslash in it is text.
	rawSingle bool
	// wordHash: `#` opens a comment only where a word starts, so `$#`,
	// `${#a}` and `a#b` are code.
	wordHash bool
	// quoteDoubling: inside a `'…'` string, two quotes in a row are one
	// literal quote and end nothing.
	quoteDoubling bool
	// scalarStart: a quote opens a string only where a scalar starts, at the
	// input's or a line's start or after one of `:-[{,?`; anywhere else it is
	// the apostrophe of a plain scalar.
	scalarStart bool
}

var (
	python = hashDialect{triple: true, lineStrings: true}
	shell  = hashDialect{rawSingle: true, wordHash: true}
	toml   = hashDialect{triple: true, lineStrings: true, rawSingle: true}
	ruby   = hashDialect{lineStrings: true}
	yaml   = hashDialect{lineStrings: true, rawSingle: true, wordHash: true, quoteDoubling: true, scalarStart: true}
)

// PythonTokens is Tokens for Python source. Strings are `'…'` and `"…"`, which
// end at their line, and their triple-quoted forms, which span lines; a
// backslash escapes the byte after it in every one of them, raw strings
// included, because a raw string's backslash still keeps a quote from closing
// it. The `r`, `b`, `f` and `rb` prefixes are letters before the quote and
// change nothing about where a literal starts or stops. An f-string's
// replacement fields are not lexed as code: a quote inside one comes in a
// matched pair, so where the literal ends is still right.
func PythonTokens(src string, blankStrings, blankComments bool) string {
	return hashTokens(src, blankStrings, blankComments, python)
}

// ShellTokens is Tokens for shell scripts. `#` opens a comment only at the
// start of a word. A `'…'` string is raw and a `"…"` string escapes; both may
// span lines. A backslash outside a string escapes the byte after it, so
// `don\'t` opens nothing.
func ShellTokens(src string, blankStrings, blankComments bool) string {
	return hashTokens(src, blankStrings, blankComments, shell)
}

// TOMLTokens is Tokens for TOML. A literal `'…'` string is raw and a basic
// `"…"` string escapes; each ends at its line, and each has a triple-quoted
// form that spans lines.
func TOMLTokens(src string, blankStrings, blankComments bool) string {
	return hashTokens(src, blankStrings, blankComments, toml)
}

// RubyTokens is Tokens for Ruby source. Strings are `'…'` and `"…"`, each with
// backslash escapes, and a `#` outside one opens a comment to the end of the
// line. A string ends at its line: a heredoc or a multi-line literal is not
// followed across lines, so a stray quote cannot blank the rest of the file.
func RubyTokens(src string, blankStrings, blankComments bool) string {
	return hashTokens(src, blankStrings, blankComments, ruby)
}

// YAMLTokens is Tokens for YAML. A `#` opens a comment only at the start of a
// word, so `a#b` in a URL is text. A `'…'` string is raw and holds `”` as one
// quote, a `"…"` string escapes, and either opens only where a scalar starts:
// the apostrophe in `msg: don't` is part of a plain scalar and opens nothing.
func YAMLTokens(src string, blankStrings, blankComments bool) string {
	return hashTokens(src, blankStrings, blankComments, yaml)
}

// hashTokens walks the source once with a range loop, carrying the lexer's
// state from byte to byte; no index is ever stepped by hand, so no mutation
// of the scan can walk it backwards.
func hashTokens(src string, blankStrings, blankComments bool, d hashDialect) string {
	b := []byte(src)
	var (
		in      byte // 0 in code, '#' in a comment, else the open string's quote
		triple  bool // the open string is triple-quoted
		escaped bool // the byte before this one was an escaping backslash
		skip    int  // the rest of a triple quote's delimiter still to pass
	)
	blank := func(cond bool, i int) {
		if cond && b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i, c := range b {
		switch {
		case skip > 0:
			skip--
		case escaped:
			escaped = false
			blank(in != 0 && blankStrings, i)
		case in == '#':
			if c == '\n' {
				in = 0
			}
			blank(blankComments, i)
		case in != 0:
			switch {
			case d.quoteDoubling && c == '\'' && in == '\'' && i+1 < len(b) && b[i+1] == '\'':
				escaped = true
				blank(blankStrings, i)
			case c == in && (!triple || bytes.HasPrefix(b[i:], []byte{c, c, c})):
				in = 0
				if triple {
					skip = 2
				}
			case c == '\n' && !triple && d.lineStrings:
				in = 0
			case c == '\\' && (in == '"' || !d.rawSingle):
				escaped = true
				blank(blankStrings, i)
			default:
				blank(blankStrings, i)
			}
		case c == '\\':
			escaped = true
		case c == '#' && (!d.wordHash || wordStart(b, i)):
			in = '#'
			blank(blankComments, i)
		case (c == '\'' || c == '"') && (!d.scalarStart || scalarStart(b, i)):
			in = c
			triple = d.triple && bytes.HasPrefix(b[i:], []byte{c, c, c})
			if triple {
				skip = 2
			}
		}
	}
	return string(b)
}

// wordStart reports whether b[i] begins a shell word: it opens the input or
// follows a blank, a newline or an operator character.
func wordStart(b []byte, i int) bool {
	return i == 0 || bytes.IndexByte([]byte(" \t\n;&|()<>"), b[i-1]) >= 0
}

// scalarStart reports whether b[i] stands where a YAML scalar can start: the
// nearest byte before it on its line that is not a blank is absent, or is one
// of the indicators `:-[{,?`.
func scalarStart(b []byte, i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch b[j] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		}
		return bytes.IndexByte([]byte(":-[{,?"), b[j]) >= 0
	}
	return true
}
