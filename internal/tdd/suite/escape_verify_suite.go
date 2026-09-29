package suite

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// diffHeaderPath reads the post-image path out of a `diff --git a/x b/y` line.
// Git writes a name that holds a quote, a backslash, a control character or
// (under the default core.quotePath) a non-ASCII byte in C-quoted form —
// `diff --git "a/caf\303\251.go" "b/caf\303\251.go"` — so the last quoted
// token is decoded rather than cut at a `b/` marker.
func diffHeaderPath(line string) (string, bool) {
	const prefix = "diff --git "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if start := lastQuotedStart(rest); start >= 0 {
		name, ok := unquoteGitPath(rest[start:])
		if !ok || !strings.HasPrefix(name, "b/") {
			return "", false
		}
		return name[len("b/"):], true
	}
	i := strings.LastIndex(rest, " b/")
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[i+3:]), true
}

// lastQuotedStart returns the index of the opening quote of s's final token
// when that token is a complete C-quoted string ending at the end of s, else
// -1.
func lastQuotedStart(s string) int {
	start, inQuote := -1, false
	for i := 0; i < len(s); i++ {
		switch {
		case inQuote && s[i] == '\\':
			i++
		case s[i] == '"':
			inQuote = !inQuote
			if inQuote {
				start = i
			}
		}
	}
	if inQuote || start < 0 || !strings.HasSuffix(s, `"`) {
		return -1
	}
	return start
}

// unquoteGitPath decodes one C-quoted git path (the surrounding quotes
// included): the single-character escapes and three-digit octal bytes git
// emits. ok is false for any other escape.
func unquoteGitPath(q string) (string, bool) {
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		return "", false
	}
	body := q[1 : len(q)-1]
	single := map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '"': '"'}
	var out []byte
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			out = append(out, body[i])
			continue
		}
		i++
		if i >= len(body) {
			return "", false
		}
		if c, ok := single[body[i]]; ok {
			out = append(out, c)
			continue
		}
		if i+2 >= len(body) || !isOctal(body[i]) || !isOctal(body[i+1]) || !isOctal(body[i+2]) {
			return "", false
		}
		out = append(out, (body[i]-'0')<<6|(body[i+1]-'0')<<3|(body[i+2]-'0'))
		i += 2
	}
	return string(out), true
}

func isOctal(b byte) bool { return b >= '0' && b <= '7' }

// fixtureLawFromPath extracts the law name from a path under
// .ratchet/fixtures/<law>/..., ok=false when rel names no fixture at all.
func fixtureLawFromPath(rel string) (string, bool) {
	prefix := ratchet.FixturesDir + "/"
	if !strings.HasPrefix(rel, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(rel, prefix)
	i := strings.Index(rest, "/")
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}
