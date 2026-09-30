package lang

import (
	"strings"
	"testing"
)

func TestReadDocument_ValuesOfEveryKind(t *testing.T) {
	doc, err := readDocument("a = \"x\\ty\\n\\r\\\"q\\\\ \\d\"\nb = 'lit\\n'\nc = true\nd = false\ne = 12\nf = [\"p\", 'q' ,\"r\"]\ng = []\nh = \"a # b\" # trailing\n")
	if err != nil {
		t.Fatal(err)
	}
	k := doc.root.keys
	if got := k["a"].s; got != "x\ty\n\r\"q\\ \\d" {
		t.Errorf("basic string = %q", got)
	}
	if got := k["b"].s; got != `lit\n` {
		t.Errorf("literal string = %q", got)
	}
	if !k["c"].b || k["d"].b || k["c"].kind != kindBool {
		t.Errorf("booleans = %+v %+v", k["c"], k["d"])
	}
	if k["e"].n != 12 || k["e"].kind != kindInt {
		t.Errorf("integer = %+v", k["e"])
	}
	if got := strings.Join(k["f"].list, "|"); got != "p|q|r" {
		t.Errorf("list = %q", got)
	}
	if k["g"].kind != kindList || len(k["g"].list) != 0 {
		t.Errorf("empty list = %+v", k["g"])
	}
	if got := k["h"].s; got != "a # b" {
		t.Errorf("a # inside a string is text: %q", got)
	}
	if got := strings.Join(doc.root.seen, ","); got != "a,b,c,d,e,f,g,h" {
		t.Errorf("declaration order = %s", got)
	}
}

func TestReadDocument_AnEmptyStringOfEitherKindIsAValue(t *testing.T) {
	doc, err := readDocument("a = ''\nb = \"\"\nc = ['', \"\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	k := doc.root.keys
	if k["a"].kind != kindString || k["a"].s != "" || k["b"].kind != kindString || k["b"].s != "" {
		t.Errorf("empty strings = %+v %+v", k["a"], k["b"])
	}
	if len(k["c"].list) != 2 || k["c"].list[0] != "" || k["c"].list[1] != "" {
		t.Errorf("list of empty strings = %q", k["c"].list)
	}
}

func TestReadDocument_TablesKeepFileOrderAndDottedNames(t *testing.T) {
	doc, err := readDocument("# head\n\n[string.b]\nopen = \"x\"\n[string.a]\nopen = \"y\"\n[comments]\n")
	if err != nil {
		t.Fatal(err)
	}
	subs := doc.subsections("string")
	if len(subs) != 2 || subs[0].name != "string.b" || subs[1].name != "string.a" {
		t.Errorf("subsections = %+v", subs)
	}
	if doc.section("comments") == nil || doc.section("string") != nil {
		t.Error("section finds an exact name only")
	}
	if got := doc.subsections("str"); len(got) != 0 {
		t.Errorf("a prefix is a whole segment: %v", got)
	}
}

func TestReadDocument_CRLFReadsLikeLF(t *testing.T) {
	doc, err := readDocument("name = \"x\"\r\n[comments]\r\nline = [\"#\"]\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if doc.section("comments").keys["line"].list[0] != "#" {
		t.Error("a CRLF file reads like an LF one")
	}
}

func TestReadDocument_Errors(t *testing.T) {
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
		_, err := readDocument(c.text)
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
	doc, _ := readDocument("a = 1\nb = \"x\"\n")
	f := newFields(doc.root, "f.toml")
	f.str("a")
	err := f.finish()
	if err == nil || !strings.Contains(err.Error(), "f.toml: [root] a (line 1): expects a string, not a integer") {
		t.Errorf("err = %v", err)
	}
	g := newFields(doc.root, "f.toml")
	g.num("a")
	err = g.finish()
	if err == nil || !strings.Contains(err.Error(), "b (line 2): unknown key") {
		t.Errorf("an unread key is reported: %v", err)
	}
}

func TestFields_OnlyTheFirstKindErrorIsKept(t *testing.T) {
	doc, _ := readDocument("a = 1\nb = 2\n")
	f := newFields(doc.root, "f.toml")
	f.str("a")
	f.str("b")
	if err := f.finish(); err == nil || !strings.Contains(err.Error(), "] a (") {
		t.Errorf("err = %v, want the first mismatch (a)", err)
	}
}

func TestFields_ReadsEachKind(t *testing.T) {
	doc, _ := readDocument("s = \"v\"\nn = 3\nb = true\nl = [\"x\"]\n")
	f := newFields(doc.root, "f.toml")
	if f.str("s") != "v" || f.num("n") != 3 || !f.flag("b") || f.list("l")[0] != "x" {
		t.Error("a getter returned the wrong value")
	}
	if f.str("missing") != "" || f.num("missing") != 0 || f.flag("missing") || f.list("missing") != nil {
		t.Error("a missing key reads as the zero value")
	}
	if err := f.finish(); err != nil {
		t.Error(err)
	}
	if !f.has("s") || f.has("missing") {
		t.Error("has reports presence")
	}
}

func TestKind_NamesStateWhatWasExpected(t *testing.T) {
	want := map[kind]string{kindString: "string", kindBool: "boolean", kindList: "array of strings", kindInt: "integer"}
	for k, w := range want {
		if k.String() != w {
			t.Errorf("kind %d = %q, want %q", k, k.String(), w)
		}
	}
}
