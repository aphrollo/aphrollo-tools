package tdd

// mask blanks the contents of string literals and comments in source code,
// replacing each masked byte with a space while preserving length, byte
// offsets, and newlines. Smell detectors run against the masked copy so a
// pattern that only appears inside a string or comment — `"call setTimeout"`,
// `// assert x == x`, a test description mentioning `.only` — never trips a
// blocking gate. Uniform masking across every detector is the single biggest
// false-positive fix in the port: the original hooks masked inconsistently, so
// any test that merely *mentioned* a smell in prose got blocked.
//
// Handled: single quotes, double quotes, backticks (JS template / Go raw
// strings), C-style line (//) and block (/* */) comments, and shell/Python
// `#` line comments. A backslash-escaped byte inside a '...' or "..." string
// is skipped, so a string like "say \"x\"" does not terminate early at the
// escaped quote and leak its tail as code — without that, an escaped quote
// could EXPOSE a smell and cause a false edit-time block. Backticks are raw
// (no escapes), so escapes are not processed there.
//
// This is a deliberately small lexer, not a full multi-language parser: the
// executable `${...}` inside JS template literals is not special-cased. That
// is acceptable because masking only ever makes a gate MORE permissive — it
// can hide a real smell, never invent one — and the authoritative TDD wall
// lives at commit/push, not at edit time.
//
// mask blanks BOTH strings and comments, treating `#` as a line comment. The
// bare helpers default to `#`-as-comment so direct callers and the masker's own
// tests keep their existing behavior; the policy engine instead derives the
// `#` rule per file via maskTokens (see lang in policy.go), because `#` is a
// comment in Python/Ruby but a private-field sigil in JS/TS and absent in Go.
func mask(src string) string { return maskTokens(src, true, true, true) }

// maskTokens is the shared lexer. It always RECOGNISES strings and C-style
// comments (so a `//` inside a string is not mistaken for a comment, and a
// quote inside a comment does not start a string), but only BLANKS the
// categories requested. `#` is treated as a line comment only when hashComment
// is set — otherwise it is ordinary code, so a JS private field (`this.#x`)
// does not make the masker skip the rest of the line and leak a quoted
// directive past the string-blanking. Recognition is mandatory; blanking is
// selective.
func maskTokens(src string, blankStrings, blankComments, hashComment bool) string {
	b := []byte(src)
	n := len(b)
	blank := func(cond bool, i int) {
		if cond && b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i := 0; i < n; i++ {
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
