package mask

// Package mask blanks the contents of string literals and comments in source
// code, replacing each masked byte with a space while preserving length, byte
// offsets, and newlines, so a pattern that only appears inside a string or a
// comment never trips a check about CODE.
//
// It lives in its own package because two engines need the same answer: the
// edit-time smell detectors (internal/tdd) and the law engine
// (internal/ratchet, `mask_strings` in the law schema). A second
// implementation of a lexer this fiddly is a second set of edge cases —
// escaped quotes, raw strings, `#` as a sigil rather than a comment — and
// they would drift.

import (
	"bytes"
	"unicode/utf8"
)

// StringsAndComments blanks BOTH strings and comments, treating `#` as a line
// comment.
//
// Handled: single quotes, double quotes, backticks (JS template / Go raw
// strings), C-style line (//) and block (/* */) comments, and shell/Python
// `#` line comments. A backslash-escaped byte inside a '...' or "..." string
// is skipped, so a string like "say \"x\"" does not terminate early at the
// escaped quote and leak its tail as code — without that, an escaped quote
// could EXPOSE a token and cause a false block. Backticks are raw (no
// escapes), so escapes are not processed there.
//
// This is a deliberately small lexer, not a full multi-language parser: the
// executable `${...}` inside JS template literals is not special-cased. That
// is acceptable because masking only ever makes a check MORE permissive — it
// can hide a real finding, never invent one.
func StringsAndComments(src string) string { return Tokens(src, true, true, true) }

// Tokens is the shared lexer. It always RECOGNISES strings and C-style
// comments (so a `//` inside a string is not mistaken for a comment, and a
// quote inside a comment does not start a string), but only BLANKS the
// categories requested. `#` is treated as a line comment only when hashComment
// is set — otherwise it is ordinary code, so a JS private field (`this.#x`)
// does not make the masker skip the rest of the line and leak a quoted
// directive past the string-blanking. Recognition is mandatory; blanking is
// selective.
func Tokens(src string, blankStrings, blankComments, hashComment bool) string {
	return tokens(src, blankStrings, blankComments, hashComment, false)
}

// RustTokens is Tokens for Rust source, where `'` is never a string quote. It
// opens a char literal only when the literal closes on the same line in a
// char literal's shape: one char (`'x'`, `'é'`) or one escape (`'\n'`,
// `'\x7f'`, `'\u{1F600}'`, an escaped quote). Any other `'` is the sigil of
// a lifetime or a loop label (`<'_>`, `&'a T`, `'static`, `break 'outer`) and
// stays code; read as a quote, it would blank everything up to the next
// apostrophe in the file and hide those lines from every check. `#` opens an
// attribute, never a comment.
func RustTokens(src string, blankStrings, blankComments bool) string {
	return tokens(src, blankStrings, blankComments, false, true)
}

func tokens(src string, blankStrings, blankComments, hashComment, rustChars bool) string {
	b := []byte(src)
	n := len(b)
	blank := func(cond bool, i int) {
		if cond && b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i := 0; i < n; i++ {
		if rustChars && b[i] == '\'' {
			if end, ok := charLiteralEnd(b, i); ok {
				for k := i + 1; k < end; k++ {
					blank(blankStrings, k)
				}
				i = end
			}
			continue
		}
		switch b[i] {
		case '\'', '"', '`':
			quote := b[i]
			escapes := quote != '`' // backticks are raw strings
			for i++; i < n && b[i] != quote; i++ {
				if escapes && b[i] == '\\' {
					blank(blankStrings, i) // blank the backslash AND the escaped
					i++                    // byte, so an escaped quote can't end
					if i < n {             // the string early
						blank(blankStrings, i)
					}
					continue
				}
				blank(blankStrings, i)
			}
		case '/':
			if i+1 < n && b[i+1] == '/' {
				blank(blankComments, i)
				for i++; i < n && b[i] != '\n'; i++ {
					blank(blankComments, i)
				}
			} else if i+1 < n && b[i+1] == '*' {
				blank(blankComments, i)
				blank(blankComments, i+1)
				for i += 2; i < n; i++ {
					if b[i] == '*' && i+1 < n && b[i+1] == '/' {
						blank(blankComments, i)
						blank(blankComments, i+1)
						i++
						break
					}
					blank(blankComments, i)
				}
			}
		case '#':
			if !hashComment {
				continue // `#` is not a comment in this language (JS/TS/Go)
			}
			for ; i < n && b[i] != '\n'; i++ {
				blank(blankComments, i)
			}
		case '\\':
			// Zig multiline string literal: a `\\` token at line start (after
			// optional leading whitespace) begins a RAW string that runs to
			// end-of-line. It is not a delimited string, so without this an
			// inside-string smell token stays visible and can trip a false
			// edit-time block. No escapes (Zig multiline strings are raw); each
			// consecutive `\\` line is re-matched per line at its own line start.
			// A `\\` escape inside a "..."/'...' string never reaches here — the
			// string branch consumes it first — so this cannot fire on escapes.
			if i+1 < n && b[i+1] == '\\' && lineLeadingWhitespace(b, i) {
				for ; i < n && b[i] != '\n'; i++ {
					blank(blankStrings, i)
				}
			}
		}
	}
	return string(b)
}

// charLiteralEnd returns the index of the quote closing the Rust char literal
// that opens at b[i], and false when b[i] opens none. The body is one char,
// or a backslash and the escape it starts (`\n`, `\'`, `\x7f`, `\u{1F600}`),
// whose tail runs to the next quote. The search stops at the line's end: a
// char literal never spans a line.
func charLiteralEnd(b []byte, i int) (int, bool) {
	line, _, _ := bytes.Cut(b[i:], []byte{'\n'})
	j := 1
	if j < len(line) && line[j] == '\\' {
		j += 2 // the backslash and the byte it escapes, which may be a quote
		for j < len(line) && line[j] != '\'' {
			j++
		}
	} else {
		_, size := utf8.DecodeRune(line[j:])
		j += size
	}
	if j < len(line) && line[j] == '\'' {
		return i + j, true
	}
	return 0, false
}

// lineLeadingWhitespace reports whether every byte from the start of the current
// line up to (not including) i is ASCII whitespace — i.e. i is the first
// non-whitespace byte on its line. Used to anchor Zig's `\\` multiline-string
// opener to line start so a stray `\\` elsewhere is not treated as one.
func lineLeadingWhitespace(b []byte, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if b[j] == '\n' {
			return true
		}
		if b[j] != ' ' && b[j] != '\t' {
			return false
		}
	}
	return true
}
