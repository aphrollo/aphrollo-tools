package ratchet

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// SymbolPatterns is what a symbol-removed law captures names with, each with
// the one group that holds the name. A law that states its own `pattern` uses
// that one. A law that states none reads the test-declaration patterns of the
// language rows its scope names, so a language's idea of a test lives in its
// row and `test_removed` needs no per-language preset; naming no row with a
// test pattern is an error, since a law that captures nothing removes nothing
// and would read clean forever.
func (l Law) SymbolPatterns() ([]*regexp.Regexp, error) {
	if l.Matcher.Pattern != nil {
		return []*regexp.Regexp{l.Matcher.Pattern}, nil
	}
	var patterns []*regexp.Regexp
	for _, row := range l.languages().Rows() {
		if l.scopeNamesRow(row) {
			patterns = append(patterns, row.Tests...)
		}
	}
	if len(patterns) == 0 {
		return nil, fmt.Errorf("law %q states no matcher.pattern and its scope names no language row with a [tests] pattern (%s) — "+
			"state a pattern, or add the language's row", l.Name, lang.Dir)
	}
	return patterns, nil
}

// symbolPatterns is SymbolPatterns as the engine matches them: against a
// file's whole text (see wholeFileSymbolPattern).
func (l Law) symbolPatterns() ([]*regexp.Regexp, error) {
	raw, err := l.SymbolPatterns()
	if err != nil {
		return nil, err
	}
	whole := make([]*regexp.Regexp, len(raw))
	for i, p := range raw {
		whole[i] = wholeFileSymbolPattern(p)
	}
	return whole, nil
}

// scopeNamesRow reports whether one of the law's include globs names an
// extension or a file name of the row.
func (l Law) scopeNamesRow(row lang.Language) bool {
	for _, glob := range l.Scope.Include {
		glob = strings.ToLower(glob)
		for _, ext := range row.Extensions {
			if globNamesExt(glob, ext) {
				return true
			}
		}
		for _, name := range row.Filenames {
			if glob == strings.ToLower(name) || strings.HasSuffix(glob, "/"+strings.ToLower(name)) {
				return true
			}
		}
	}
	return false
}

// symbolNames is every name the patterns capture in text, in order of
// appearance within each pattern.
func symbolNames(patterns []*regexp.Regexp, text string) []string {
	var names []string
	for _, p := range patterns {
		for _, idx := range p.FindAllStringSubmatchIndex(text, -1) {
			names = append(names, text[idx[2]:idx[3]])
		}
	}
	return names
}
