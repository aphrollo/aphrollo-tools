package ratchet

import (
	"strings"
	"testing"
)

func TestParseTOMLReadsRootKeysTablesAndValueKinds(t *testing.T) {
	doc, err := parseTOML(`
# a law
name = "nan-guard"
escape_lines = 3
code_only = true

[scope]
include = ["crates/**/*.rs", "tools/**/*.rs"]
exclude = []

[matcher]
kind = 'regex-absent'
pattern = "\.clamp\("
`)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if got := doc.str("", "name"); got != "nan-guard" {
		t.Errorf("name = %q, want nan-guard", got)
	}
	if got := doc.str("matcher", "pattern"); got != `\.clamp\(` {
		t.Errorf("pattern = %q, want an unescaped regex", got)
	}
	if got := doc.str("matcher", "kind"); got != "regex-absent" {
		t.Errorf("literal string kind = %q", got)
	}
	if v, _ := doc.value("", "escape_lines"); v.kind != tomlInt || v.i != 3 {
		t.Errorf("escape_lines = %+v, want int 3", v)
	}
	if v, _ := doc.value("", "code_only"); v.kind != tomlBool || !v.b {
		t.Errorf("code_only = %+v, want bool true", v)
	}
	inc, _ := doc.value("scope", "include")
	if len(inc.list) != 2 || inc.list[0] != "crates/**/*.rs" {
		t.Errorf("include = %v", inc.list)
	}
	exc, _ := doc.value("scope", "exclude")
	if exc.kind != tomlArray || len(exc.list) != 0 {
		t.Errorf("empty array = %+v", exc)
	}
}

func TestParseTOML_NamesTheNestedTableAndItsLine(t *testing.T) {
	_, err := parseTOML("name = \"x\"\n\n[matcher.extra]\na = \"x\"\n")
	if err == nil || err.Error() != "line 3: nested table [matcher.extra] — a law file is one level deep" {
		t.Fatalf("err = %v", err)
	}
}

func TestParseTOML_ListsTablesInFileOrderWithTheirKeys(t *testing.T) {
	doc, err := parseTOML("top = 1\n[zeta]\nb = 1\na = 2\n[alpha]\nc = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(doc.sectionNames(), ","); got != "zeta,alpha" {
		t.Errorf("sectionNames = %s, want zeta,alpha", got)
	}
	if got := strings.Join(doc.keys("zeta"), ","); got != "b,a" {
		t.Errorf("keys(zeta) = %s, want b,a", got)
	}
	if got := strings.Join(doc.keys(""), ","); got != "top" {
		t.Errorf("root keys = %s, want top", got)
	}
	if !doc.has("alpha") || doc.has("beta") {
		t.Error("has reports a declared table only")
	}
	if v, _ := doc.value("alpha", "c"); v.line != 6 || !v.b {
		t.Errorf("value = %+v, want line 6 true", v)
	}
}

func TestParseTOMLRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"bare word value":     "name = nan\n",
		"missing equals":      "name\n",
		"unterminated string": "name = \"nan\n",
		"unterminated table":  "[scope\n",
		"duplicate key":       "name = \"a\"\nname = \"b\"\n",
		"duplicate table":     "[scope]\na = \"x\"\n[scope]\nb = \"y\"\n",
		"nested table":        "[scope.deep]\na = \"x\"\n",
		"mixed array":         "a = [\"x\", 1]\n",
	}
	for name, text := range cases {
		if _, err := parseTOML(text); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}
