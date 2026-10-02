package ghworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Node {
	t.Helper()
	n, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("ParseYAML: %v\n%s", err, src)
	}
	return n
}

func TestParseYAML_MapsListsAndScalarsKeepTheirValuesAndOrder(t *testing.T) {
	n := mustParse(t, `
name: Pipeline  # trailing comment
on:
  pull_request:
    types: [opened, synchronize]
  push:
    branches: [main]
env:
  A: "quoted: value"
  B: 'it''s'
  C: plain # not part of the value
jobs:
  build:
    needs: [a, b]
    steps:
      - uses: actions/checkout@v4
      - name: Test
        run: go test ./...
        env: { X: 1, Y: "two" }
      - run: echo hi
`)
	if got := n.Get("name").Text(); got != "Pipeline" {
		t.Errorf("name = %q", got)
	}
	if got := strings.Join(n.Keys, ","); got != "name,on,env,jobs" {
		t.Errorf("key order = %s", got)
	}
	if got := n.Get("env").Get("A").Text(); got != "quoted: value" {
		t.Errorf("A = %q", got)
	}
	if got := n.Get("env").Get("B").Text(); got != "it's" {
		t.Errorf("B = %q", got)
	}
	if got := n.Get("env").Get("C").Text(); got != "plain" {
		t.Errorf("C = %q, a trailing comment is not part of the value", got)
	}
	steps := n.Get("jobs").Get("build").Get("steps")
	if len(steps.Items) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps.Items))
	}
	if got := steps.Items[1].Get("env").Get("Y").Text(); got != "two" {
		t.Errorf("flow map Y = %q", got)
	}
	needs := n.Get("jobs").Get("build").Get("needs")
	if len(needs.Items) != 2 || needs.Items[1].Text() != "b" {
		t.Errorf("needs = %+v", needs)
	}
}

func TestParseYAML_BlockScalarsKeepTheirLinesAndChomping(t *testing.T) {
	n := mustParse(t, "run: |\n  set -e\n  if [ -f x ]; then\n    echo \"a # b\"\n  fi\n\nnext: |-\n  one\n  two\nfold: >\n  a\n  b\n\n  c\nkeep: |+\n  x\n\nlast: end\n")
	if got, want := n.Get("run").Text(), "set -e\nif [ -f x ]; then\n  echo \"a # b\"\nfi\n"; got != want {
		t.Errorf("run = %q, want %q", got, want)
	}
	if got := n.Get("next").Text(); got != "one\ntwo" {
		t.Errorf("next = %q", got)
	}
	if got := n.Get("fold").Text(); got != "a b\nc\n" {
		t.Errorf("fold = %q", got)
	}
	if got := n.Get("keep").Text(); got != "x\n\n" {
		t.Errorf("keep = %q", got)
	}
	if got := n.Get("last").Text(); got != "end" {
		t.Errorf("last = %q", got)
	}
}

func TestParseYAML_ASequenceMayShareItsKeysIndent(t *testing.T) {
	n := mustParse(t, "steps:\n- run: a\n- run: b\nafter: x\n")
	if got := len(n.Get("steps").Items); got != 2 {
		t.Fatalf("steps = %d, want 2", got)
	}
	if n.Get("after").Text() != "x" {
		t.Error("the key after the sequence was lost")
	}
}

func TestParseYAML_RefusesWhatItCannotReadByNamingTheLine(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"anchor", "a: &x 1\n", "line 1"},
		{"alias", "a: *x\n", "line 1"},
		{"tag", "a: !!str 1\n", "line 1"},
		{"merge key", "a:\n  <<: b\n", "line 2"},
		{"second document", "a: 1\n---\nb: 2\n", "second YAML document"},
		{"tab indent", "a:\n\tb: 1\n", "tab"},
		{"duplicate key", "a: 1\na: 2\n", "duplicate key"},
		{"multi-line plain scalar", "a: one\n  two\n", "continued on the next line"},
		{"colon in plain scalar", "a: x: y\n", "plain scalar"},
		{"unterminated quote", "a: \"x\n", "close on the same line"},
		{"multi-line flow", "a: [1,\n  2]\n", "close on the same line"},
		{"explicit key", "? a\n: b\n", "explicit key"},
		{"indent digit", "a: |2\n  x\n", "not supported"},
		{"bad indent", "a: 1\n  b: 2\n", "line 2"},
	} {
		_, err := ParseYAML(c.src)
		if err == nil {
			t.Errorf("%s: parsed, want a refusal", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}

func TestParseYAML_ThisReposOwnWorkflowsParse(t *testing.T) {
	// tree-read-ok: the real workflow files are the fixture this reader must handle
	dir := filepath.Join("..", "..", ".github", "workflows")
	files, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflow files found under %s (%v)", dir, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n, err := ParseYAML(string(data))
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
			continue
		}
		if n.Get("jobs") == nil || len(n.Get("jobs").Keys) == 0 {
			t.Errorf("%s: no jobs read", filepath.Base(f))
		}
	}
}

func TestParseYAML_DocumentMarkersAndEmptyDocuments(t *testing.T) {
	if n := mustParse(t, "---\na: 1\n"); n.Get("a").Text() != "1" {
		t.Errorf("a leading --- must be accepted: %+v", n)
	}
	for _, src := range []string{"", "\n\n", "# only a comment\n", "---\n", "---\n# nothing\n"} {
		if n := mustParse(t, src); n.Kind != KindNull {
			t.Errorf("%q parsed as kind %d, want null", src, n.Kind)
		}
	}
	for _, src := range []string{"a: 1\n...\n", "a: 1\n---\n", "- a\n---\n- b\n"} {
		if _, err := ParseYAML(src); err == nil || !strings.Contains(err.Error(), "second YAML document") {
			t.Errorf("%q: err = %v, want the second document refused", src, err)
		}
	}
	if _, err := ParseYAML("a: 1\nstray\n"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("stray text after the document: err = %v", err)
	}
	if _, err := ParseYAML("just a scalar\n"); err == nil {
		t.Error("a scalar document was accepted")
	}
}

func TestParseYAML_NullsEmptyValuesAndKeyForms(t *testing.T) {
	n := mustParse(t, "a:\nb: ~\nc: null\n\"quoted key\": 1\n'single': 2\nd:\n  - x\ne: 'text' # c\n")
	for _, k := range []string{"a", "b", "c"} {
		if n.Get(k) == nil || n.Get(k).Kind != KindNull {
			t.Errorf("%s = %+v, want null", k, n.Get(k))
		}
	}
	if n.Get("quoted key").Text() != "1" || n.Get("single").Text() != "2" {
		t.Errorf("quoted keys not read: %v", n.Keys)
	}
	if got := n.Get("d").Items[0].Text(); got != "x" {
		t.Errorf("d[0] = %q", got)
	}
	if n.Get("e").Text() != "text" || !n.Get("e").Quoted {
		t.Errorf("e = %+v, want a quoted scalar", n.Get("e"))
	}
	if n.Get("nosuch") != nil || (*Node)(nil).Get("a") != nil || mustParse(t, "- a").Get("a") != nil {
		t.Error("Get on an absent key, a nil node or a list must be nil")
	}
	if (*Node)(nil).Text() != "" || mustParse(t, "a: [1]").Get("a").Text() != "" {
		t.Error("Text on nil or a non-scalar must be empty")
	}
}
