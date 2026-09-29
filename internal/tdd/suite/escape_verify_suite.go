package suite

import (
	"regexp"
	"strconv"
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
	if loc := lastQuotedToken.FindStringIndex(rest); loc != nil {
		name, ok := unquoteGitPath(rest[loc[0]:])
		if !ok || !strings.HasPrefix(name, "b/") {
			return "", false
		}
		return strings.TrimPrefix(name, "b/"), true
	}
	_, post, found := cutLast(rest, " b/")
	if !found {
		return "", false
	}
	return strings.TrimSpace(post), true
}

// cutLast splits s around the last occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// lastQuotedToken matches a complete C-quoted string ending at the end of the
// line.
var lastQuotedToken = regexp.MustCompile(`"(?:[^"\\]|\\.)*"$`)

// gitQuotedBody is what git's C-quoting may hold: any byte but a backslash, or
// one of the single-character escapes, or a three-digit octal byte.
var gitQuotedBody = regexp.MustCompile(`^(?:[^\\]|\\(?:[abfnrtv\\"]|[0-3][0-7]{2}))*$`)

var gitEscape = regexp.MustCompile(`\\(?:[abfnrtv\\"]|[0-3][0-7]{2})`)

var gitSingleEscapes = map[byte]string{'a': "\a", 'b': "\b", 'f': "\f", 'n': "\n", 'r': "\r", 't': "\t", 'v': "\v", '\\': "\\", '"': `"`}

// unquoteGitPath decodes one C-quoted git path (the surrounding quotes
// included): the single-character escapes and three-digit octal bytes git
// emits. ok is false for any other escape.
func unquoteGitPath(q string) (string, bool) {
	body, ok := strings.CutPrefix(q, `"`)
	if !ok {
		return "", false
	}
	body, ok = strings.CutSuffix(body, `"`)
	if !ok || !gitQuotedBody.MatchString(body) {
		return "", false
	}
	return gitEscape.ReplaceAllStringFunc(body, func(esc string) string {
		if c, single := gitSingleEscapes[esc[1]]; single {
			return c
		}
		n, _ := strconv.ParseUint(esc[1:], 8, 8)
		return string([]byte{byte(n)})
	}), true
}

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
