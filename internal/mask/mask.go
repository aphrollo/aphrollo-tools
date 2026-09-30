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
// they would drift. The one lexer (lex.go) reads a row of the language table
// (internal/lang), so a language's spelling is data, not code.

import (
	"fmt"
	"sync"
	"unicode/utf8"

	"bytes"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// StringsAndComments blanks BOTH strings and comments, treating `#` as a line
// comment, with the language-neutral rules Tokens describes.
//
// A backslash-escaped byte inside a '...' or "..." string is skipped, so a
// string like "say \"x\"" does not terminate early at the escaped quote and
// leak its tail as code — without that, an escaped quote could EXPOSE a token
// and cause a false block. Backticks are raw (no escapes).
//
// This is a deliberately small lexer, not a full multi-language parser: the
// executable `${...}` inside JS template literals is not special-cased. That
// is acceptable because masking only ever makes a check MORE permissive — it
// can hide a real finding, never invent one.
func StringsAndComments(src string) string { return Tokens(src, true, true, true) }

var (
	neutralOnce sync.Once
	neutrals    map[string]*Lexer
)

// neutral is the compiled default row, or the default row that also reads `#`
// as a comment. They ship in the binary and a test parses them, so a failure
// to read them is a build defect and stops loudly.
func neutral(name string) *Lexer {
	neutralOnce.Do(func() {
		tbl, err := lang.Defaults()
		if err != nil {
			panic(fmt.Sprintf("mask: the embedded language table does not load: %v", err))
		}
		neutrals = map[string]*Lexer{}
		for _, n := range []string{lang.Neutral, lang.NeutralHash} {
			row, ok := tbl.Named(n)
			if !ok {
				panic(fmt.Sprintf("mask: the embedded language table has no %q row", n))
			}
			neutrals[n] = NewLexer(row)
		}
	})
	return neutrals[name]
}

// Tokens is the language-neutral lexer. It always RECOGNISES strings and
// C-style comments (so a `//` inside a string is not mistaken for a comment,
// and a quote inside a comment does not start a string), but only BLANKS the
// categories requested. `#` is treated as a line comment only when hashComment
// is set — otherwise it is ordinary code, so a JS private field (`this.#x`)
// does not make the masker skip the rest of the line and leak a quoted
// directive past the string-blanking. Recognition is mandatory; blanking is
// selective.
func Tokens(src string, blankStrings, blankComments, hashComment bool) string {
	name := lang.Neutral
	if hashComment {
		name = lang.NeutralHash
	}
	return neutral(name).Lex(src, blankStrings, blankComments)
}

// ForFile is the lexer that reads file under scan view `view` (0 is the
// current one): its row's, or the default row when the file has none.
func ForFile(t *lang.Table, file string, view int) *Lexer {
	return NewLexer(t.LexRow(file, view))
}

// CommentsForFile is the lexer that blanks file's comments for a law whose
// comment marker is `//` (see lang.Table.CommentRow).
func CommentsForFile(t *lang.Table, file string, view int) *Lexer {
	return NewLexer(t.CommentRow(file, view))
}

// charLiteralLen returns the offset from b[i] of the quote closing the char
// literal that opens at b[i], and false when b[i] opens none. The body is one
// char, or a backslash and the escape it starts (`\n`, `\'`, `\x7f`,
// `\u{1F600}`), whose tail runs to the next quote. The search stops at the
// line's end: a char literal never spans a line.
func charLiteralLen(b []byte, i int) (int, bool) {
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
		return j, true
	}
	return 0, false
}
