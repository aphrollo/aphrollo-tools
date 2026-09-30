// Package lang is the language table: one row per source language, written as
// TOML, holding everything the tools know about how that language is spelled —
// the extensions and file names it owns, how its comments and strings open and
// close, the directives that silence a quality gate, and the shape of a test
// declaration. The defaults are embedded; a repository adds or replaces a row
// with `.ratchet/languages/<name>.toml`. The lexer in internal/mask, the law
// engine, the suppression smell and the test matchers all read this one table,
// so a new language is a file, not a change to Go code.
package lang

import "regexp"

// Escape is how a string form spells a literal delimiter or control byte.
type Escape string

const (
	// EscapeNone: nothing is special inside the string but its closer.
	EscapeNone Escape = "none"
	// EscapeBackslash: a backslash makes the byte after it part of the string.
	EscapeBackslash Escape = "backslash"
	// EscapeDoubling: two closers in a row are one literal closer and end
	// nothing, and a backslash is text.
	EscapeDoubling Escape = "doubling"
)

// Directive kinds name the quality gate a suppression silences.
const (
	KindLint     = "lint"
	KindType     = "type"
	KindCoverage = "coverage"
)

// Language is one row of the table.
type Language struct {
	// Name is the row's identity and its file's stem.
	Name string
	// Extensions are the lowercase suffixes, dot included, the row owns.
	Extensions []string
	// Filenames are exact base names the row owns, for files with no suffix.
	Filenames []string
	// CodeEscape: a backslash outside a string escapes the byte after it, so
	// `don\'t` in shell opens no string.
	CodeEscape bool
	// View is the scan-view version the row's lexing took effect at: a
	// baseline stamped with an earlier version was written by lexers that read
	// this row's files as the default row, so a law over them is judged by
	// that reading until it migrates. 1, the default, means it always was.
	View int

	LineComments  []LineComment
	BlockComments []BlockComment
	Strings       []StringForm
	Suppress      []Directive
	// Tests are test-declaration patterns; each captures the test's name in
	// its one group.
	Tests []*regexp.Regexp
}

// LineComment is a marker that opens a comment ending at the line's end.
type LineComment struct {
	Marker string
	// WordStart: the marker opens a comment only where a word starts — at the
	// input's start, or after a blank, a newline or one of `;&|()<>` — so
	// `$#`, `${#a}` and `a#b` are code.
	WordStart bool
}

// BlockComment is an opener and closer pair.
type BlockComment struct {
	Open, Close string
	// Nested: an opener inside the comment opens another level that needs its
	// own closer.
	Nested bool
}

// StringForm is one way a language writes a string literal.
type StringForm struct {
	// ID is the form's name within its row, for messages.
	ID string
	// Open and Close delimit the literal; Close ends it on its first
	// occurrence (subject to Escape).
	Open, Close string
	Escape      Escape
	// Multiline: the literal runs across line ends. Otherwise it ends at its
	// line, which stops an unterminated quote from blanking the file below.
	Multiline bool
	// LineStart: the literal opens only where its line starts.
	LineStart bool
	// OpensAfter, when set or LineStart is, limits the opener to a line's
	// start or to a place whose nearest non-blank byte before it, on its
	// line, is one of these (YAML: a quote opens a scalar, not an apostrophe).
	OpensAfter string
	// BlankOpen: the opener is masked with the body (a Zig `\\` line).
	BlankOpen bool
	// CharLiteral: Open is a one-byte quote that starts a literal only when a
	// char literal's shape closes on the same line — one char or one escape —
	// and is otherwise code (Rust's lifetime and loop-label sigil).
	CharLiteral bool
}

// Directive is a comment that silences a quality gate.
type Directive struct {
	ID   string
	Kind string
	// Pattern finds the directive in the comment-preserving view of a file.
	Pattern *regexp.Regexp
	// Reason, when set, is what the rest of the directive's comment must match
	// for the suppression to be admitted as justified (a disable comment that
	// carries its why after a `--`); nil admits nothing, the directive always
	// counts.
	Reason *regexp.Regexp
}
