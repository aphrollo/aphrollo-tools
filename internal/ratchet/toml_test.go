package ratchet

import "testing"

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
