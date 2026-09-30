package ratchet

import "github.com/aphrollo/aphrollo-tools/internal/lang"

// commentPrefix is what opens a comment in the language this law scans: the
// law's own comment_prefix, else the line comment marker the language table
// gives the law's scope (see tablePrefix), else the neutral row's `//`.
func (l Law) commentPrefix() string {
	if l.CommentPrefix != "" {
		return l.CommentPrefix
	}
	if l.scopePrefix != "" {
		return l.scopePrefix
	}
	return "//"
}

// tablePrefix is the line comment marker the rows of tbl name for the law's
// scope. Each include glob votes for the first line marker of each row it
// names, where a row that does not lex comments, like a glob that names no
// row, votes for the neutral row's marker. The scope has a marker only when
// every vote is the same: a law over Rust and Python sources, or over source
// and prose, has no single prefix and reads the neutral row's.
func (l Law) tablePrefix(tbl *lang.Table) string {
	neutral := neutralMarker(tbl)
	votes := map[string]bool{}
	for _, glob := range l.Scope.Include {
		named := false
		for _, row := range tbl.Rows() {
			if globNamesRow(glob, row) {
				named = true
				votes[rowMarker(row, neutral)] = true
			}
		}
		if !named {
			votes[neutral] = true
		}
	}
	if len(votes) != 1 {
		return neutral
	}
	for marker := range votes {
		return marker
	}
	return neutral
}

// rowMarker is the first line comment marker of a row that has one, else the
// neutral marker: a row that declares no comments is lexed as the neutral row.
func rowMarker(row lang.Language, neutral string) string {
	if len(row.LineComments) > 0 {
		return row.LineComments[0].Marker
	}
	return neutral
}

// neutralMarker is the first line comment marker of the neutral row, the
// comment syntax of every file no row claims.
func neutralMarker(tbl *lang.Table) string {
	if row, ok := tbl.Named(lang.Neutral); ok && len(row.LineComments) > 0 {
		return row.LineComments[0].Marker
	}
	return "//"
}
