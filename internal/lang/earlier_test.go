package lang

import (
	"strings"
	"testing"
)

func TestParse_AHeredocFormNeedsNoCloser(t *testing.T) {
	l, err := Parse("name = \"x\"\n[string.doc]\nopen = \"<<<\"\nheredoc = true\n", "x.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Strings) != 1 || !l.Strings[0].Heredoc || l.Strings[0].Close != "" || !l.Strings[0].Multiline {
		t.Fatalf("strings = %+v, want one multiline heredoc form with no closer", l.Strings)
	}
}

func TestParse_AHeredocFormRefusesWhatItCannotMean(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"a closer":     {"close = \"X\"\n", "[string.doc] a heredoc closes on its identifier, not on a `close`"},
		"an escape":    {"escape = \"backslash\"\n", "[string.doc] a heredoc takes no escape"},
		"a char lit":   {"char_literal = true\n", "[string.doc] a heredoc is not a char literal"},
		"a line start": {"line_start = true\n", "[string.doc] a heredoc opens wherever its operator stands"},
		"opens after":  {"opens_after = \"=\"\n", "[string.doc] a heredoc opens wherever its operator stands"},
		"a blank open": {"blank_open = true\n", "[string.doc] a heredoc keeps its head"},
	}
	for name, c := range cases {
		_, err := Parse("name = \"x\"\n[string.doc]\nopen = \"<<<\"\nheredoc = true\n"+c.body, "x.toml")
		if err == nil || !strings.Contains(err.Error(), "x.toml: "+c.want) {
			t.Errorf("%s: err = %v, want it to say %q", name, err, c.want)
		}
	}
}

func TestParse_ALineExceptionMustBeginWithALineMarker(t *testing.T) {
	l, err := Parse("name = \"x\"\n[comments]\nline = [\"//\", \"#\"]\nline_except = [\"#[\", \"#!\"]\n", "x.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.LineComments) != 2 || len(l.LineExcept) != 2 || l.LineExcept[0] != "#[" || l.LineExcept[1] != "#!" {
		t.Fatalf("line comments = %+v, exceptions = %q, want both exceptions in order", l.LineComments, l.LineExcept)
	}
	for name, text := range map[string]string{
		"no marker at all":   "name = \"x\"\n[comments]\nline_except = [\"#[\"]\n",
		"another marker":     "name = \"x\"\n[comments]\nline = [\"//\"]\nline_except = [\"#[\"]\n",
		"the marker itself":  "name = \"x\"\n[comments]\nline = [\"#\"]\nline_except = [\"#\"]\n",
		"an empty exception": "name = \"x\"\n[comments]\nline = [\"#\"]\nline_except = [\"\"]\n",
	} {
		_, err := Parse(text, "x.toml")
		if err == nil || !strings.Contains(err.Error(), "x.toml: [comments] line_except") {
			t.Errorf("%s: err = %v, want a line_except error", name, err)
		}
	}
}

func chainRow(name, earlier string, view int) string {
	text := "name = \"" + name + "\"\nview = " + string(rune('0'+view)) + "\n"
	if earlier != "" {
		text += "earlier = \"" + earlier + "\"\n"
	}
	if name == "a" {
		text += "extensions = [\".a\"]\n"
	}
	return text + "[comments]\nline = [\"#\"]\n"
}

func TestLexRow_ARowReadsByItsEarlierRowsAtTheViewsBeforeIt(t *testing.T) {
	var rows []Language
	for _, text := range []string{chainRow("a", "b", 3), chainRow("b", "c", 2), chainRow("c", "", 1)} {
		rows = append(rows, mustRow(t, text))
	}
	tbl, err := (&Table{}).Extend(append(rows, mustRow(t, "name = \"default\"\n[comments]\nline = [\"//\"]\n")))
	if err != nil {
		t.Fatal(err)
	}
	for view, want := range map[int]string{0: "a", 4: "a", 3: "a", 2: "b", 1: "c"} {
		if got := tbl.LexRow("x.a", view).Name; got != want {
			t.Errorf("LexRow at view %d = %s, want %s", view, got, want)
		}
	}
	if row, ok := tbl.For("x.a"); !ok || row.Name != "a" {
		t.Errorf("For(x.a) = %s, %v: an earlier row owns no extension", row.Name, ok)
	}
}

func TestLexRow_AChainThatEndsBeforeTheViewReadsTheNeutralRow(t *testing.T) {
	a, b := mustRow(t, chainRow("a", "b", 3)), mustRow(t, chainRow("b", "", 2))
	tbl, err := (&Table{}).Extend([]Language{a, b, mustRow(t, "name = \"default\"\n[comments]\nline = [\"//\"]\n")})
	if err != nil {
		t.Fatal(err)
	}
	if got := tbl.LexRow("x.a", 1).Name; got != "default" {
		t.Errorf("LexRow at view 1 = %s, want the neutral row: the chain began at view 2", got)
	}
}

func TestExtend_RefusesAnEarlierRowThatCannotBeRead(t *testing.T) {
	for name, c := range map[string]struct {
		rows []string
		want string
	}{
		"unknown": {[]string{chainRow("a", "ghost", 3)}, `language "a": earlier "ghost" names no row`},
		"itself":  {[]string{chainRow("a", "a", 3)}, `language "a": earlier names the row itself`},
		"a cycle": {[]string{chainRow("a", "b", 3), chainRow("b", "a", 2)}, `language "a": earlier rows form a cycle`},
	} {
		var rows []Language
		for _, text := range c.rows {
			rows = append(rows, mustRow(t, text))
		}
		if _, err := (&Table{}).Extend(rows); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestDefaults_ThePHPRowReadsAsItsEarlierRowBeforeViewFour(t *testing.T) {
	tbl, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	for view, want := range map[int]string{0: "php", 4: "php", 3: "php-v3", 2: "default", 1: "default"} {
		if got := tbl.LexRow("a.php", view).Name; got != want {
			t.Errorf("LexRow(a.php, %d) = %s, want %s", view, got, want)
		}
	}
	if row, _ := tbl.For("a.php"); row.Name != "php" || row.Earlier != "php-v3" {
		t.Errorf("For(a.php) = %s earlier %q, want php reading php-v3", row.Name, row.Earlier)
	}
	if got := tbl.ViewFor(func(ext string) bool { return ext == ".php" }, false); got != 4 {
		t.Errorf("ViewFor(.php) = %d, want 4", got)
	}
}
