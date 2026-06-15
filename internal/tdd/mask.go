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
// `#` line comments. This is a deliberately small lexer, not a full
// multi-language parser: escaped quotes inside strings and the executable
// `${...}` inside JS template literals are not special-cased. That is
// acceptable because masking only ever makes a gate MORE permissive (it can
// hide a real smell, never invent one), and the authoritative TDD wall lives
// at commit/push, not at edit time.
func mask(src string) string {
	b := []byte(src)
	n := len(b)
	blank := func(i int) {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i := 0; i < n; i++ {
		switch b[i] {
		case '\'', '"', '`':
			quote := b[i]
			for i++; i < n && b[i] != quote; i++ {
				blank(i)
			}
		case '/':
			if i+1 < n && b[i+1] == '/' {
				blank(i)
				for i++; i < n && b[i] != '\n'; i++ {
					blank(i)
				}
			} else if i+1 < n && b[i+1] == '*' {
				blank(i)
				blank(i + 1)
				for i += 2; i < n; i++ {
					if b[i] == '*' && i+1 < n && b[i+1] == '/' {
						blank(i)
						blank(i + 1)
						i++
						break
					}
					blank(i)
				}
			}
		case '#':
			for ; i < n && b[i] != '\n'; i++ {
				blank(i)
			}
		}
	}
	return string(b)
}
