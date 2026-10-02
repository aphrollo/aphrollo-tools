package ghworkflow

import "testing"

func TestParseYAML_FlowValuesNestAndRefuseWhatIsBroken(t *testing.T) {
	n := mustParse(t, "m: [[1, 2], {a: b, 'c': \"d\"}, 'e', ]\nempty: []\nem: {}\nl:\n  -\n    k: v\n  - plain\n")
	m := n.Get("m")
	if len(m.Items) != 3 || m.Items[0].Items[1].Text() != "2" || m.Items[1].Get("a").Text() != "b" || m.Items[1].Get("c").Text() != "d" || m.Items[2].Text() != "e" {
		t.Errorf("m = %+v", m)
	}
	if len(n.Get("empty").Items) != 0 || n.Get("empty").Kind != KindList || n.Get("em").Kind != KindMap {
		t.Errorf("empty flow values: %+v %+v", n.Get("empty"), n.Get("em"))
	}
	if l := n.Get("l"); len(l.Items) != 2 || l.Items[0].Get("k").Text() != "v" || l.Items[1].Text() != "plain" {
		t.Errorf("l = %+v", l)
	}
	for _, src := range []string{
		"a: [1, 2\n", "a: {x}\n", "a: {x: 1\n", "a: [&x 1]\n", "a: {x: *y}\n",
		"a: [1]]\n", "a: [1] tail\n", "a: 'x' tail\n", "a: {'k' 1}\n", "a: ['unterminated]\n",
		"- - x\n", "a:\n  - - x\n", "a: {x: [1}\n", "a: [{]\n",
	} {
		if _, err := ParseYAML(src); err == nil {
			t.Errorf("%q parsed, want a refusal", src)
		}
	}
}

func TestParseYAML_QuotedScalarsHandleEscapesAndComments(t *testing.T) {
	src := "a: \"tab\\there \\\"q\\\" \\u0041 \\x42 \\\\ \\/ nl\\n\"\n" +
		"b: 'it''s # not a comment' # but this is\n" +
		"c: \"has # hash\" # real comment\n" +
		"d: it's fine\n" +
		"e: x#y\n"
	n := mustParse(t, src)
	if got, want := n.Get("a").Text(), "tab\there \"q\" A B \\ / nl\n"; got != want {
		t.Errorf("a = %q, want %q", got, want)
	}
	if got := n.Get("b").Text(); got != "it's # not a comment" {
		t.Errorf("b = %q", got)
	}
	if got := n.Get("c").Text(); got != "has # hash" {
		t.Errorf("c = %q", got)
	}
	if n.Get("d").Text() != "it's fine" || n.Get("e").Text() != "x#y" {
		t.Errorf("d/e = %q / %q", n.Get("d").Text(), n.Get("e").Text())
	}
	for _, bad := range []string{
		"a: \"bad \\q\"\n", "a: \"short \\u00\"\n", "a: \"bad \\uzzzz\"\n", "a: \"trail\\\n", "a: \"x\\xZZ\"\n",
	} {
		if _, err := ParseYAML(bad); err == nil {
			t.Errorf("%q parsed, want an escape refusal", bad)
		}
	}
}

func TestParseYAML_BlockScalarEdgeCases(t *testing.T) {
	src := "empty: |\nnext: 1\n" +
		"indented: |\n    deep\n      deeper\n    back\n" +
		"lit: |\n  a\n\n\n  b\n" +
		"clip: |\n  x\n\n\n" +
		"fold: >-\n  one\n    indented\n  two\n\n  three\n" +
		"tail: end\n"
	n := mustParse(t, src)
	if n.Get("empty").Text() != "" || n.Get("next").Text() != "1" {
		t.Errorf("empty block: %q / %q", n.Get("empty").Text(), n.Get("next").Text())
	}
	if got := n.Get("indented").Text(); got != "deep\n  deeper\nback\n" {
		t.Errorf("indented = %q", got)
	}
	if got := n.Get("lit").Text(); got != "a\n\n\nb\n" {
		t.Errorf("lit = %q", got)
	}
	if got := n.Get("clip").Text(); got != "x\n" {
		t.Errorf("clip = %q", got)
	}
	if got := n.Get("fold").Text(); got != "one\n  indented\ntwo\nthree" {
		t.Errorf("fold = %q", got)
	}
	if n.Get("tail").Text() != "end" {
		t.Error("the key after the blocks was lost")
	}
}

func TestParseYAML_ListItemShapes(t *testing.T) {
	n := mustParse(t, "a:\n  - k: v\n    j: w\n  - 'q': 1\n  - plain text\n  - \"dq\"\n  - [x, y]\n  - {m: n}\nb:\n- k: 1\n")
	items := n.Get("a").Items
	if len(items) != 6 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].Get("k").Text() != "v" || items[0].Get("j").Text() != "w" || items[1].Get("q").Text() != "1" {
		t.Errorf("map items = %+v %+v", items[0], items[1])
	}
	if items[2].Text() != "plain text" || items[3].Text() != "dq" || items[4].Items[1].Text() != "y" || items[5].Get("m").Text() != "n" {
		t.Errorf("scalar and flow items wrong: %+v", items)
	}
	if n.Get("b").Items[0].Get("k").Text() != "1" {
		t.Error("a sequence at its key's indent was lost")
	}
}

func TestParseYAML_StructuralRefusals(t *testing.T) {
	for name, src := range map[string]string{
		"list item where a key belongs": "a: 1\n- b\n",
		"key then deeper junk":          "a:\n    b: 1\n  c: 2\n",
		"unexpected indentation in seq": "- a\n  - b\n",
		"seq under key then outdent":    "a:\n  - x\n b: 1\n",
		"line without a colon":          "a: 1\nb\n",
	} {
		if _, err := ParseYAML(src); err == nil {
			t.Errorf("%s: %q parsed, want a refusal", name, src)
		}
	}
}

func TestParseYAML_FlowEdgeCases(t *testing.T) {
	if n := mustParse(t, "a: [,]\n"); len(n.Get("a").Items) != 1 || n.Get("a").Items[0].Text() != "" {
		t.Errorf("a lone comma is one empty item: %+v", n.Get("a"))
	}
	if n := mustParse(t, "a: {: x}\n"); n.Get("a").Get("").Text() != "x" {
		t.Errorf("an empty flow key: %+v", n.Get("a"))
	}
	if n := mustParse(t, "a: [1 2]\n"); len(n.Get("a").Items) != 1 || n.Get("a").Items[0].Text() != "1 2" {
		t.Errorf("a plain scalar with a space is one item: %+v", n.Get("a"))
	}
	for _, src := range []string{
		"a: ['a' 'b']\n", "a: {k: 'x' y}\n", "a: ['x'\n", "a: {abc\n", "a: [abc\n", "a: {k: v\n",
	} {
		if _, err := ParseYAML(src); err == nil {
			t.Errorf("%q parsed, want a refusal", src)
		}
	}
}
