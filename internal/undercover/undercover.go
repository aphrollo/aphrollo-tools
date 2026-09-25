package undercover

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// List is the built-in tells plus a workspace's own extra tokens.
type List struct {
	extra []extraToken
}

// extraToken is one `undercover-extra` entry: its tokens for a ref name, a
// whole-word pattern for prose.
type extraToken struct {
	name   string
	tokens []string
	line   *regexp.Regexp
}

// New builds the list with the workspace's extra tokens. An empty entry is
// skipped: it would match nothing as a word and everything as a sequence.
func New(extra []string) List {
	var l List
	for _, raw := range extra {
		name := strings.ToLower(strings.TrimSpace(raw))
		toks := splitTokens(name)
		if len(toks) == 0 {
			continue
		}
		l.extra = append(l.extra, extraToken{
			name:   name,
			tokens: toks,
			line:   regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`),
		})
	}
	return l
}

// Load reads the workspace's switch and extra tokens from root: the
// `[workspace.metadata.aphrollo]` table of Cargo.toml, else the `[aphrollo]`
// table of aphrollo.toml. on is false unless one of them says
// `undercover = true`, and every check is inert then.
func Load(root string) (l List, on bool) {
	cargo := filepath.Join(root, "Cargo.toml")
	toml := filepath.Join(root, "aphrollo.toml")
	on = core.TomlBoolIn(cargo, "[workspace.metadata.aphrollo]", "undercover") ||
		core.TomlBoolIn(toml, "[aphrollo]", "undercover")
	extra := append(core.TomlStringsIn(cargo, "[workspace.metadata.aphrollo]", "undercover-extra"),
		core.TomlStringsIn(toml, "[aphrollo]", "undercover-extra")...)
	return New(extra), on
}

// Line reports the first tell one line of prose carries.
func (l List) Line(s string) (tell string, hit bool) {
	scrubbed := scrub(s)
	for _, t := range Tells {
		if t.Line == nil {
			continue
		}
		subject := s
		if t.Scrubbed {
			subject = scrubbed
		}
		if t.Line.MatchString(subject) {
			return t.Name, true
		}
	}
	for _, e := range l.extra {
		if e.line.MatchString(s) {
			return e.name, true
		}
	}
	return "", false
}

// Hit is one line of a body that carries a tell.
type Hit struct {
	LineNo int
	Line   string
	Tell   string
}

// Text reports the first line of body that carries a tell, numbered from 1.
func (l List) Text(body string) (Hit, bool) {
	for i, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if tell, ok := l.Line(line); ok {
			return Hit{LineNo: i + 1, Line: strings.TrimSpace(line), Tell: tell}, true
		}
	}
	return Hit{}, false
}

// Ident reports the tell a git identity (`git var GIT_AUTHOR_IDENT`: name,
// address, timestamp) carries.
func (l List) Ident(ident string) (tell string, hit bool) {
	return l.Line(ident)
}

// RefName reports the tell a ref name carries. The name is split on `/`, `-`,
// `_` and `.` and matched token by token, so `lane/cairo` never matches a
// word inside a word, and `Claude_x` still does.
func (l List) RefName(name string) (tell string, hit bool) {
	toks := splitTokens(strings.ToLower(name))
	for i, tok := range toks {
		for _, t := range Tells {
			if !hasToken(t.Tokens, tok) {
				continue
			}
			if t.Versioned && !versionFollows(toks, i) {
				continue
			}
			return tok, true
		}
		for _, e := range l.extra {
			if i+len(e.tokens) <= len(toks) && equalTokens(toks[i:i+len(e.tokens)], e.tokens) {
				return e.name, true
			}
		}
	}
	return "", false
}

// RefRefusal is the one line every ref wall prints.
func RefRefusal(kind, name, tell string) string {
	return fmt.Sprintf("undercover: the %s name %q carries %q, which this repo keeps out of its history; name it lane/<slug> instead", kind, name, tell)
}

// TextRefusal is the refusal for a line of text about to be posted.
func TextRefusal(kind string, h Hit) string {
	return fmt.Sprintf("undercover: the %s, line %d, carries %q:\n    %s\nRewrite the line to say what changed, then try again.", kind, h.LineNo, h.Tell, h.Line)
}

// IdentRefusal is the refusal for a commit identity carrying a tell.
func IdentRefusal(role, ident, tell string) string {
	return fmt.Sprintf("undercover: the %s identity %q carries %q, which this repo keeps out of its history.\nSet your own: git config user.name \"<name>\" && git config user.email \"<address>\" (--global for every repo), then commit again.", role, identNameAddress(ident), tell)
}

// identNameAddress drops the timestamp `git var` appends to an identity.
func identNameAddress(ident string) string {
	if nameAddress, _, found := strings.Cut(ident, ">"); found {
		return nameAddress + ">"
	}
	return strings.TrimSpace(ident)
}

// guidanceFileName is the one place the word is a FILE, not a tell: a repo
// whose operating instructions live in CLAUDE.md has to be able to say so.
var guidanceFileName = regexp.MustCompile(`(?i)\bclaude\.md\b`)

// refOrPath is a slash-joined token carrying the word: a branch
// (`lane/claude-md`) or a directory (`.claude/skills`) is a name in the repo,
// not a tell. A URL is never one of those.
var refOrPath = regexp.MustCompile(`(?i)(\S+/claude[\w.-]*|\.claude/\S*)`)

func scrub(line string) string {
	line = guidanceFileName.ReplaceAllString(line, "the guidance file")
	return refOrPath.ReplaceAllStringFunc(line, func(m string) string {
		if strings.Contains(m, "://") {
			return m
		}
		return "a repo name"
	})
}

func splitTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.'
	})
}

func hasToken(set []string, tok string) bool {
	for _, s := range set {
		if s == tok {
			return true
		}
	}
	return false
}

func equalTokens(a, b []string) bool {
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// versionFollows reports whether the token after toks[i] is a version.
func versionFollows(toks []string, i int) bool {
	return i+1 < len(toks) && startsWithDigit(toks[i+1])
}

func startsWithDigit(s string) bool {
	return s != "" && s[0] >= '0' && s[0] <= '9'
}
