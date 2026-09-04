package guardrail

// mask blanks the contents of quoted strings and trailing `#` comments in a
// shell command, replacing them with spaces while preserving length and the
// surrounding structure. This keeps policy matching from firing on a tool name
// or `sleep` that only appears inside a string literal or a comment.
//
// Command substitutions ($(...), backticks) are intentionally left intact: they
// are executed, so a `sleep` inside one is a real blocking wait. This is a
// deliberately small masker, not a full shell parser; escaped quotes inside
// strings are not handled (acceptable for a non-security advisory guardrail).
func mask(command string) string {
	b := []byte(command)
	var inSingle, inDouble bool
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				b[i] = ' '
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				b[i] = ' '
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '#' && (i == 0 || b[i-1] == ' ' || b[i-1] == '\t' || b[i-1] == '\n'):
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		}
	}
	return string(b)
}
