package lang

import (
	"regexp"
	"strings"
)

// DeclaresTest reports whether line declares a test in a file of extension ext
// (lowercase, dot included), and whether the table knows the language's test
// declarations at all: a row with no declaration patterns, like an extension
// no row owns, answers "unknown" so a caller can err toward running the suite.
func (t *Table) DeclaresTest(ext, line string) (decl, known bool) {
	i, ok := t.byExt[ext]
	if !ok || len(t.rows[i].Declarations) == 0 {
		return false, false
	}
	for _, re := range t.rows[i].Declarations {
		if re.MatchString(line) {
			return true, true
		}
	}
	return false, true
}

// TestNames lists the test names the row's test patterns capture in a file's
// whole text: each pattern's names in order of appearance, the patterns in
// declaration order.
func (l Language) TestNames(text string) []string {
	return namesIn(l.Tests, text)
}

// SelectableNames is TestNames read by the row's selectable patterns, else by
// its test patterns: the names a runner can be asked to run.
func (l Language) SelectableNames(text string) []string {
	if len(l.Selectable) == 0 {
		return namesIn(l.Tests, text)
	}
	return namesIn(l.Selectable, text)
}

func namesIn(patterns []*regexp.Regexp, text string) []string {
	var names []string
	for _, p := range patterns {
		for _, m := range WholeFile(p).FindAllStringSubmatch(text, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

// WholeFile recompiles a test pattern to match against a file's whole text
// rather than one physical line at a time — the idiomatic Rust layout puts
// `#[test]` on its own line above the `fn` it marks, and a line-by-line scan
// never joins the two into one match. `(?m)` is prepended so a pattern
// anchored with `^` or `$` still binds to each line's start or end within the
// file rather than the file's as a whole; a pattern that already opens with
// its own `(?...)` flag group is trusted to have chosen its own semantics and
// is returned as it is.
func WholeFile(p *regexp.Regexp) *regexp.Regexp {
	src := p.String()
	if strings.HasPrefix(src, "(?") {
		return p
	}
	return regexp.MustCompile("(?m)" + src)
}
