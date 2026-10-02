package ghworkflow

import (
	"strings"
	"testing"
)

func TestParseYAML_EndOfInputAfterAKeyOrADashIsNull(t *testing.T) {
	if n := mustParse(t, "a:\n"); n.Get("a") == nil || n.Get("a").Kind != KindNull {
		t.Errorf("a key at the end of input = %+v, want null", n.Get("a"))
	}
	if n := mustParse(t, "a:"); n.Get("a") == nil || n.Get("a").Kind != KindNull {
		t.Errorf("a key with no trailing newline = %+v, want null", n.Get("a"))
	}
	n := mustParse(t, "l:\n  -\n  - x\n  -\n")
	items := n.Get("l").Items
	if len(items) != 3 || items[0].Kind != KindNull || items[1].Text() != "x" || items[2].Kind != KindNull {
		t.Errorf("a dash with nothing after it is a null item: %+v", items)
	}
	if n := mustParse(t, "l:\n  -\n    k: v\n"); n.Get("l").Items[0].Get("k").Text() != "v" {
		t.Error("a dash followed by a deeper block takes that block")
	}
}

func TestParseYAML_CommentsAndQuotesDoNotHideEachOther(t *testing.T) {
	src := "a: \"q\\\" # hash\"\n" +
		"b: 'back\\' # comment\n" +
		"c: x# y\n" +
		"d: v #c\n" +
		"e: v\t#c\n" +
		"f: w # c\n"
	n := mustParse(t, src)
	for k, want := range map[string]string{"a": "q\" # hash", "b": "back\\", "c": "x# y", "d": "v", "e": "v", "f": "w"} {
		if got := n.Get(k).Text(); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestParseYAML_ScalarsEndingInAColonAndLoneQuestionMarks(t *testing.T) {
	if n := mustParse(t, "a: b:\n"); n.Get("a").Text() != "b:" {
		t.Errorf("a = %q, want a plain scalar ending in a colon", n.Get("a").Text())
	}
	if _, err := ParseYAML("?\n"); err == nil || !strings.Contains(err.Error(), "explicit key") {
		t.Errorf("a lone ? must be refused as an explicit key: %v", err)
	}
	if n := mustParse(t, "?x: 1\n"); n.Get("?x").Text() != "1" {
		t.Error("a key that merely starts with ? is a plain key")
	}
}

func TestParseYAML_ShortEscapesNameTheirLine(t *testing.T) {
	if _, err := ParseYAML("a: \"\\x41"); err == nil || !strings.Contains(err.Error(), "close on the same line") {
		t.Errorf("an escape that fits exactly but is never closed must say the string is unterminated, got %v", err)
	}
	if _, err := ParseYAML("a: \"\\x4\""); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("a two-digit escape with one digit: err = %v", err)
	}
	if n := mustParse(t, "a: \"\\x41\"\n"); n.Get("a").Text() != "A" {
		t.Error("a complete escape must decode")
	}
}
