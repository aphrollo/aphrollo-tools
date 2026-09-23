package tdd

import (
	"os"
	"strings"
)

// tomlStringsIn reads one string-ARRAY key from one table of a TOML file,
// sorted and deduped; empty for an absent key or an unreadable manifest. Same
// line scanner as tomlBoolIn, and same reasoning: the key sits directly under
// its table in any real manifest, and a parse miss costs only the feature
// staying off.
func tomlStringsIn(path, table, key string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	inTable, inArray := false, false
	var pkgs []string
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if !inArray && strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		if !inTable {
			continue
		}
		if !inArray {
			k, val, found := strings.Cut(trimmed, "=")
			if !found || strings.TrimSpace(k) != key {
				continue
			}
			inArray = true
			trimmed = val
		}
		pkgs = append(pkgs, quotedWords(trimmed)...)
		// The array's OWN closing bracket, never one an entry's quoted
		// reason happens to mention — "start indexes '[' and end indexes
		// ']'" is a real accept-list reason, and closing on it dropped
		// every entry after it (issue #139).
		if strings.Contains(stripQuoted(trimmed), "]") {
			inArray = false
		}
	}
	return dedupeSorted(pkgs)
}

// stripQuoted removes every double-quoted run from s, so a scan for TOML
// PUNCTUATION (an array's closing `]`, a table header's `[`) never mistakes
// one living inside a quoted VALUE for the syntax itself.
func stripQuoted(s string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '"')
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:open])
		rest := s[open+1:]
		_, end, ok := basicStringBody(rest)
		if !ok {
			// An unterminated quote consumes the rest of the line as
			// string content — nothing after it is punctuation either.
			return b.String()
		}
		s = rest[end+1:]
	}
}

// quotedWords returns the contents of every double-quoted run in s, in order.
func quotedWords(s string) []string {
	var out []string
	for {
		open := strings.IndexByte(s, '"')
		if open < 0 {
			return out
		}
		rest := s[open+1:]
		body, end, ok := basicStringBody(rest)
		if !ok {
			return out
		}
		out = append(out, body)
		s = rest[end+1:]
	}
}

// basicStringBody reads one TOML basic string from just past its opening
// quote: the unescaped content, the index of the closing quote in s, and
// whether one was found. A backslash escapes the character after it, so a
// reason quoting code (`if x == \"\"`) keeps its quotes instead of ending on
// them. \" and \\ unescape to the character; any other escape is kept as
// written, which no reader of these lists depends on.
func basicStringBody(s string) (string, int, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), i, true
		case '\\':
			if i+1 >= len(s) {
				return "", 0, false
			}
			i++
			if s[i] != '"' && s[i] != '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(s[i])
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, false
}
