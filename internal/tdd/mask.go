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
// mask blanks BOTH strings and comments. Detectors that match executable code
// (a sleep call, a self-comparison, it.only) use it. Suppression detectors
// need comments PRESERVED — //nolint, // @ts-ignore, # type: ignore all live
// in comments — so they call maskStrings instead, which blanks strings only.
func mask(src string) string { return maskTokens(src, true, true) }

// maskStrings blanks string literals while leaving comments intact, for
// detectors that look for directives written in comments. Strings are still
// blanked so a directive quoted in a string (`"see // nolint"`) cannot trip.
func maskStrings(src string) string { return maskTokens(src, true, false) }

// maskTokens is the shared lexer. It always RECOGNISES strings and comments (so
// a `//` inside a string is not mistaken for a comment, and a quote inside a
// comment does not start a string), but only BLANKS the categories requested.
// Recognition is mandatory; blanking is selective.
func maskTokens(src string, blankStrings, blankComments bool) string {
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
			for ; i < n && b[i] != '\n'; i++ {
				blank(blankComments, i)
			}
		}
	}
	return string(b)
}
