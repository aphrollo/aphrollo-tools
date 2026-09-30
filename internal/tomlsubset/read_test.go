package tomlsubset

import (
	"strings"
	"testing"
)

// The reader and its tests moved here from internal/lang; Parse replaced
// readDocument, and five tests took its name.
// ratchet: test_removed TestReadDocument_ValuesOfEveryKind: renamed TestParse_ValuesOfEveryKind with the reader's move to internal/tomlsubset
// ratchet: test_removed TestReadDocument_AnEmptyStringOfEitherKindIsAValue: renamed TestParse_AnEmptyStringOfEitherKindIsAValue with the reader's move
// ratchet: test_removed TestReadDocument_TablesKeepFileOrderAndDottedNames: renamed TestParse_TablesKeepFileOrderAndDottedNames with the reader's move
// ratchet: test_removed TestReadDocument_CRLFReadsLikeLF: renamed TestParse_CRLFReadsLikeLF with the reader's move
// ratchet: test_removed TestReadDocument_Errors: renamed TestParse_Errors with the reader's move

func TestParse_ValuesOfEveryKind(t *testing.T) {
	doc, err := Parse("a = \"x\\ty\\n\\r\\\"q\\\\ \\d\"\nb = 'lit\\n'\nc = true\nd = false\ne = 12\nf = [\"p\", 'q' ,\"r\"]\ng = []\nh = \"a # b\" # trailing\n")
	if err != nil {
		t.Fatal(err)
	}
	k := doc.Root.Keys
	if got := k["a"].S; got != "x\ty\n\r\"q\\ \\d" {
		t.Errorf("basic string = %q", got)
	}
	if got := k["b"].S; got != `lit\n` {
		t.Errorf("literal string = %q", got)
	}
	if !k["c"].B || k["d"].B || k["c"].Kind != Bool {
		t.Errorf("booleans = %+v %+v", k["c"], k["d"])
	}
	if k["e"].N != 12 || k["e"].Kind != Int {
		t.Errorf("integer = %+v", k["e"])
	}
	if got := strings.Join(k["f"].List, "|"); got != "p|q|r" {
		t.Errorf("list = %q", got)
	}
	if k["g"].Kind != List || len(k["g"].List) != 0 {
		t.Errorf("empty list = %+v", k["g"])
	}
	if got := k["h"].S; got != "a # b" {
		t.Errorf("a # inside a string is text: %q", got)
	}
	if got := strings.Join(doc.Root.Seen, ","); got != "a,b,c,d,e,f,g,h" {
		t.Errorf("declaration order = %s", got)
	}
}

func TestParse_AnEmptyStringOfEitherKindIsAValue(t *testing.T) {
	doc, err := Parse("a = ''\nb = \"\"\nc = ['', \"\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	k := doc.Root.Keys
	if k["a"].Kind != String || k["a"].S != "" || k["b"].Kind != String || k["b"].S != "" {
		t.Errorf("empty strings = %+v %+v", k["a"], k["b"])
	}
	if len(k["c"].List) != 2 || k["c"].List[0] != "" || k["c"].List[1] != "" {
		t.Errorf("list of empty strings = %q", k["c"].List)
	}
}

func TestParse_TablesKeepFileOrderAndDottedNames(t *testing.T) {
	doc, err := Parse("# head\n\n[string.b]\nopen = \"x\"\n[string.a]\nopen = \"y\"\n[comments]\n")
	if err != nil {
		t.Fatal(err)
	}
	subs := doc.Subsections("string")
	if len(subs) != 2 || subs[0].Name != "string.b" || subs[1].Name != "string.a" {
		t.Errorf("subsections = %+v", subs)
	}
	if doc.Section("comments") == nil || doc.Section("string") != nil {
		t.Error("section finds an exact name only")
	}
	if got := doc.Subsections("str"); len(got) != 0 {
		t.Errorf("a prefix is a whole segment: %v", got)
	}
	if subs[0].Line != 3 || subs[1].Line != 5 {
		t.Errorf("table lines = %d, %d, want 3, 5", subs[0].Line, subs[1].Line)
	}
}

func TestParse_CRLFReadsLikeLF(t *testing.T) {
	doc, err := Parse("name = \"x\"\r\n[comments]\r\nline = [\"#\"]\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Section("comments").Keys["line"].List[0] != "#" {
		t.Error("a CRLF file reads like an LF one")
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]struct{ text, want string }{
		"no equals":          {"a\n", "line 1: expected `key = value`"},
		"quoted key":         {"\"a\" = 1\n", "not a bare key"},
		"dotted key":         {"a.b = 1\n", "not a bare key"},
		"empty key":          {"= 1\n", "not a bare key"},
		"missing value":      {"a =\n", "line 1: missing value"},
		"bare word":          {"a = yes\n", `"yes" is not a value`},
		"float":              {"a = 1.5\n", `"1.5" is not a value`},
		"trailing":           {"a = \"x\" y\n", "trailing text after a string"},
		"unterminated":       {"a = \"x\n", "unterminated string"},
		"unterminated lit":   {"a = 'x\n", "unterminated literal string"},
		"unterminated list":  {"a = [\"x\"\n", "unterminated array"},
		"list of numbers":    {"a = [1]\n", "array elements are quoted strings"},
		"list missing comma": {"a = [\"x\" \"y\"]\n", "expected `,` between array elements"},
		"list bad string":    {"a = [\"x]\n", "unterminated string"},
		"header open":        {"[a\n", "unterminated table header"},
		"header empty":       {"[]\n", "not a bare table name"},
		"header space":       {"[a b]\n", "not a bare table name"},
		"header quoted":      {"[\"a\"]\n", "not a bare table name"},
		"header leading dot": {"[.a]\n", "not a bare table name"},
		"header trail dot":   {"[a.]\n", "not a bare table name"},
		"header double dot":  {"[a..b]\n", "not a bare table name"},
		"header nested list": {"[[a]]\n", "not a bare table name"},
		"duplicate key":      {"a = 1\na = 2\n", "line 2: key \"a\" is set twice"},
		"duplicate table":    {"[a]\n[a]\n", "line 2: table [a] is declared twice"},
	}
	for name, c := range cases {
		_, err := Parse(c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, c.want)
		}
	}
}

func TestDropComment_StopsAtAHashOutsideStrings(t *testing.T) {
	cases := map[string]string{
		`a # c`:              `a `,
		`"x#y" # c`:          `"x#y" `,
		`'x#y' # c`:          `'x#y' `,
		`"a\"#" # c`:         `"a\"#" `,
		`'a\' # c`:           `'a\' `,
		`"a'" # c`:           `"a'" `,
		`'a"' # c`:           `'a"' `,
		`["#", "b"] # c`:     `["#", "b"] `,
		`nohash`:             `nohash`,
		`#`:                  ``,
		`"\\" # c`:           `"\\" `,
		`"a\\\" # still in"`: `"a\\\" # still in"`,
	}
	for in, want := range cases {
		if got := dropComment(in); got != want {
			t.Errorf("dropComment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFields_KindMismatchNamesTheKeyAndLine(t *testing.T) {
	doc, _ := Parse("a = 1\nb = \"x\"\n")
	f := NewFields(doc.Root, "f.toml")
	f.Str("a")
	err := f.Finish()
	if err == nil || !strings.Contains(err.Error(), "f.toml: [root] a (line 1): expects a string, not a integer") {
		t.Errorf("err = %v", err)
	}
	g := NewFields(doc.Root, "f.toml")
	g.Num("a")
	err = g.Finish()
	if err == nil || !strings.Contains(err.Error(), "b (line 2): unknown key") {
		t.Errorf("an unread key is reported: %v", err)
	}
}

func TestFields_OnlyTheFirstKindErrorIsKept(t *testing.T) {
	doc, _ := Parse("a = 1\nb = 2\n")
	f := NewFields(doc.Root, "f.toml")
	f.Str("a")
	f.Str("b")
	if err := f.Finish(); err == nil || !strings.Contains(err.Error(), "] a (") {
		t.Errorf("err = %v, want the first mismatch (a)", err)
	}
}

func TestFields_ReadsEachKind(t *testing.T) {
	doc, _ := Parse("s = \"v\"\nn = 3\nb = true\nl = [\"x\"]\n")
	f := NewFields(doc.Root, "f.toml")
	if f.Str("s") != "v" || f.Num("n") != 3 || !f.Flag("b") || f.List("l")[0] != "x" {
		t.Error("a getter returned the wrong value")
	}
	if f.Str("missing") != "" || f.Num("missing") != 0 || f.Flag("missing") || f.List("missing") != nil {
		t.Error("a missing key reads as the zero value")
	}
	if err := f.Finish(); err != nil {
		t.Error(err)
	}
	if !f.Has("s") || f.Has("missing") {
		t.Error("Has reports presence")
	}
}

func TestKind_NamesStateWhatWasExpected(t *testing.T) {
	want := map[Kind]string{String: "string", Bool: "boolean", List: "array of strings", Int: "integer"}
	for k, w := range want {
		if k.String() != w {
			t.Errorf("kind %d = %q, want %q", k, k.String(), w)
		}
	}
}

func TestParse_ALawFileShape(t *testing.T) {
	doc, err := Parse("# a law\nname = \"nan-guard\"\nescape_lines = 3\ncode_only = true\n\n[scope]\ninclude = [\"crates/**/*.rs\"]\nexclude = []\n\n[matcher]\nkind = 'regex-absent'\npattern = \"\\.clamp\\(\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("matcher").Keys["pattern"].S; got != `\.clamp\(` {
		t.Errorf("pattern = %q, want an unescaped regex", got)
	}
	if got := doc.Root.Keys["escape_lines"]; got.Kind != Int || got.N != 3 || got.Line != 3 {
		t.Errorf("escape_lines = %+v", got)
	}
	if got := strings.Join(doc.Section("scope").Seen, ","); got != "include,exclude" {
		t.Errorf("scope key order = %s", got)
	}
}
