package lang

import (
	"strings"
	"testing"
)

const minimal = `name = "tiny"
`

func TestParse_ReadsEveryField(t *testing.T) {
	text := `name = "demo"
extensions = [".dm", ".dmx"]
filenames = ["Demofile"]
code_escape = true
view = 4

[comments]
line = ["//", "#"]
line_word_start = true
block = ["/* */", "{- -}"]
block_nested = true

[string.triple]
open = '"""'
escape = "backslash"
multiline = true

[string.quote]
open = "'"
close = "'"
escape = "doubling"
opens_after = ":-"
line_start = true
blank_open = true

[suppress.off]
kind = "lint"
pattern = 'demo:\s*off'
reason = '--\s+\S'

[tests]
patterns = ['def (\w+)']
`
	l, err := Parse(text, "demo.toml")
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "demo" || !l.CodeEscape || l.View != 4 {
		t.Errorf("root = %q code_escape=%v view=%d", l.Name, l.CodeEscape, l.View)
	}
	if got := strings.Join(l.Extensions, ","); got != ".dm,.dmx" {
		t.Errorf("extensions = %q", got)
	}
	if got := strings.Join(l.Filenames, ","); got != "Demofile" {
		t.Errorf("filenames = %q", got)
	}
	if len(l.LineComments) != 2 || l.LineComments[1] != (LineComment{Marker: "#", WordStart: true}) {
		t.Errorf("line comments = %+v", l.LineComments)
	}
	if len(l.BlockComments) != 2 || l.BlockComments[1] != (BlockComment{Open: "{-", Close: "-}", Nested: true}) {
		t.Errorf("block comments = %+v", l.BlockComments)
	}
	if len(l.Strings) != 2 {
		t.Fatalf("string forms = %+v", l.Strings)
	}
	// The triple quote sorts before the single one it begins with.
	if l.Strings[0].ID != "triple" || l.Strings[0].Close != `"""` || l.Strings[0].Escape != EscapeBackslash || !l.Strings[0].Multiline {
		t.Errorf("triple = %+v", l.Strings[0])
	}
	q := l.Strings[1]
	if q.Escape != EscapeDoubling || q.OpensAfter != ":-" || !q.LineStart || !q.BlankOpen || q.Multiline || q.CharLiteral {
		t.Errorf("quote = %+v", q)
	}
	if len(l.Suppress) != 1 || l.Suppress[0].Kind != KindLint || !l.Suppress[0].Pattern.MatchString("demo: off") || !l.Suppress[0].Reason.MatchString("-- why") {
		t.Errorf("suppress = %+v", l.Suppress)
	}
	if len(l.Tests) != 1 || l.Tests[0].FindStringSubmatch("def foo")[1] != "foo" {
		t.Errorf("tests = %v", l.Tests)
	}
}

func TestParse_Defaults(t *testing.T) {
	l, err := Parse(minimal, "tiny.toml")
	if err != nil {
		t.Fatal(err)
	}
	if l.View != 1 {
		t.Errorf("view = %d, want 1 when omitted", l.View)
	}
	if l.CodeEscape || len(l.Strings) != 0 {
		t.Errorf("a bare row reads no escape and no strings: %+v", l)
	}
	row, err := Parse("name = \"x\"\n[string.s]\nopen = \"'\"\n", "x.toml")
	if err != nil {
		t.Fatal(err)
	}
	s := row.Strings[0]
	if s.Close != "'" || s.Escape != EscapeNone || s.Multiline {
		t.Errorf("a form defaults to closing on its opener, no escape, ending at its line: %+v", s)
	}
}

func TestParse_RejectsWhatItCannotRead(t *testing.T) {
	cases := map[string]struct{ text, want string }{
		"no name":             {"extensions = [\".x\"]\n", `name ""`},
		"uppercase name":      {"name = \"Big\"\n", `name "Big"`},
		"name with a slash":   {"name = \"a/b\"\n", `name "a/b"`},
		"extension no dot":    {"name = \"x\"\nextensions = [\"rs\"]\n", `extension "rs"`},
		"extension just dot":  {"name = \"x\"\nextensions = [\".\"]\n", `extension "."`},
		"extension uppercase": {"name = \"x\"\nextensions = [\".RS\"]\n", `extension ".RS"`},
		"extension with path": {"name = \"x\"\nextensions = [\".a/b\"]\n", `extension ".a/b"`},
		"filename with path":  {"name = \"x\"\nfilenames = [\"a/b\"]\n", `file name "a/b"`},
		"empty filename":      {"name = \"x\"\nfilenames = [\"\"]\n", `file name ""`},
		"view zero":           {"name = \"x\"\nview = 0\n", "view 0"},
		"unknown root key":    {"name = \"x\"\nextnsions = []\n", "unknown key"},
		"wrong kind":          {"name = \"x\"\nextensions = \"a\"\n", "expects a array of strings, not a string"},
		"unknown table":       {"name = \"x\"\n[comment]\nline = [\"#\"]\n", "unknown table [comment]"},
		"unknown comment key": {"name = \"x\"\n[comments]\nlines = [\"#\"]\n", "unknown key"},
		"empty line marker":   {"name = \"x\"\n[comments]\nline = [\"\"]\n", "empty marker"},
		"block one word":      {"name = \"x\"\n[comments]\nblock = [\"/*\"]\n", `"/*" must be an opener and a closer`},
		"block three words":   {"name = \"x\"\n[comments]\nblock = [\"a b c\"]\n", `"a b c" must be an opener and a closer`},
		"string no open":      {"name = \"x\"\n[string.s]\nescape = \"none\"\n", "needs an `open`"},
		"string empty close":  {"name = \"x\"\n[string.s]\nopen = \"'\"\nclose = \"\"\n", "close is empty"},
		"bad escape":          {"name = \"x\"\n[string.s]\nopen = \"'\"\nescape = \"caret\"\n", `escape "caret"`},
		"char literal long":   {"name = \"x\"\n[string.s]\nopen = \"''\"\nchar_literal = true\n", "char literal"},
		"char literal close":  {"name = \"x\"\n[string.s]\nopen = \"'\"\nclose = \"\\\"\"\nchar_literal = true\n", "char literal"},
		"suppress bad kind":   {"name = \"x\"\n[suppress.s]\nkind = \"style\"\npattern = \"a\"\n", `kind "style"`},
		"suppress no pattern": {"name = \"x\"\n[suppress.s]\nkind = \"lint\"\n", "pattern"},
		"suppress bad regex":  {"name = \"x\"\n[suppress.s]\nkind = \"lint\"\npattern = \"(\"\n", "not a regular expression"},
		"suppress bad reason": {"name = \"x\"\n[suppress.s]\nkind = \"lint\"\npattern = \"a\"\nreason = \"(\"\n", "reason"},
		"tests no group":      {"name = \"x\"\n[tests]\npatterns = [\"def\"]\n", "exactly one group, it has 0"},
		"tests two groups":    {"name = \"x\"\n[tests]\npatterns = [\"(a)(b)\"]\n", "exactly one group, it has 2"},
		"tests bad regex":     {"name = \"x\"\n[tests]\npatterns = [\"(\"]\n", "not a regular expression"},
		"not toml":            {"name\n", "expected `key = value`"},
		"duplicate key":       {"name = \"x\"\nname = \"y\"\n", "set twice"},
		"duplicate table":     {"name = \"x\"\n[comments]\n[comments]\n", "declared twice"},
	}
	for name, c := range cases {
		_, err := Parse(c.text, "x.toml")
		if err == nil {
			t.Errorf("%s: parsed, want an error naming %q", name, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not name %q", name, err, c.want)
		}
		if !strings.Contains(err.Error(), "x.toml") {
			t.Errorf("%s: error %q does not name the file", name, err)
		}
	}
}

func TestParse_OneCharacterExtensionIsLegal(t *testing.T) {
	if _, err := Parse("name = \"x\"\nextensions = [\".c\"]\n", "x.toml"); err != nil {
		t.Errorf("a two-byte extension is the shortest legal one: %v", err)
	}
}

func TestParse_OneGroupIsEnough(t *testing.T) {
	l, err := Parse("name = \"x\"\n[tests]\npatterns = [\"(a)\"]\n", "x.toml")
	if err != nil || len(l.Tests) != 1 {
		t.Errorf("one capture group is the required count: %v %v", l.Tests, err)
	}
}

func TestParse_LongestOpenerFirstThenDeclarationOrder(t *testing.T) {
	text := "name = \"x\"\n" +
		"[string.a]\nopen = \"'\"\n" +
		"[string.b]\nopen = \"'''\"\n" +
		"[string.c]\nopen = \"'\"\n"
	l, err := Parse(text, "x.toml")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range l.Strings {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "b,a,c" {
		t.Errorf("order = %s, want b,a,c", got)
	}
}
