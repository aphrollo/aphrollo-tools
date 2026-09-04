package ratchet

import (
	"path/filepath"
	"strings"
)

// A law's escape has to sit in a real comment, so that the token cannot forge
// from inside a string literal or unrelated prose. That rule needs to know
// what a comment IS, and a law scoped over several file kinds has no single
// answer: `transient_doc_reference` scans .rs, .md, .toml and .txt, and
// resolving one prefix from the law tested TOML and markdown against Rust
// syntax. A `# sdd-ok:` line — a real comment, in the position the law's own
// message asks for — suppressed nothing.
//
// So the prefix is resolved per FILE. A law that names its own
// comment_prefix still wins, because a law deliberately scoped to one
// language has already answered the question.

// proseExts are the file kinds with NO comment syntax. Requiring an escape to
// open a comment in one of them is unsatisfiable by construction — no edit to
// a markdown line can make it a comment — so there the escape counts inline.
// The forgery the comment rule guards against needs a string literal or a
// code line to hide in, and a prose file has neither.
var proseExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".rst": true,
}

// escapePrefixFor resolves the comment prefix the escape window should use in
// one file. prose reports that the file kind has no comment syntax at all, in
// which case the caller accepts the escape inline rather than demanding a
// comment that cannot exist.
func escapePrefixFor(l Law, file string) (prefix string, prose bool) {
	if l.CommentPrefix != "" {
		return l.CommentPrefix, false
	}
	if proseExts[strings.ToLower(filepath.Ext(file))] {
		return "", true
	}
	line, _, _ := commentSyntax(file)
	return line, false
}

// escapedInProse accepts the escape token anywhere on the trigger's line or
// within EscapeLines above it. There is no comment to open and no code to
// hide in, so position is all the file affords.
func (l Law) escapedInProse(raw []string, idx int) bool {
	if idx >= 0 && idx < len(raw) && strings.Contains(raw[idx], l.Escape) {
		return true
	}
	for i := idx - 1; i >= 0 && i >= idx-l.EscapeLines; i-- {
		if strings.Contains(raw[i], l.Escape) {
			return true
		}
	}
	return false
}
