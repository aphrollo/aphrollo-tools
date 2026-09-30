package mask

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// lexRow lexes src as the embedded row `name` with strings and comments both
// blanked unless a view is given.
func lexAs(t *testing.T, name, src string, views ...bool) string {
	t.Helper()
	tbl := defaults(t)
	row, ok := tbl.Named(name)
	if !ok {
		t.Fatalf("no language row %s", name)
	}
	bs, bc := true, true
	if len(views) == 2 {
		bs, bc = views[0], views[1]
	}
	return NewLexer(row).Lex(src, bs, bc)
}

func synth(t *testing.T, text string) *Lexer {
	t.Helper()
	row, err := lang.Parse(text, "synthetic.toml")
	if err != nil {
		t.Fatal(err)
	}
	return NewLexer(row)
}

type lexCase struct{ src, want string }

func runLexCases(t *testing.T, row string, cases map[string]lexCase) {
	t.Helper()
	for name, c := range cases {
		if got := lexAs(t, row, c.src); got != c.want {
			t.Errorf("%s %s:\n got %q\nwant %q", row, name, got, c.want)
		}
	}
}

func TestLex_JavaTextBlocksCharsAndComments(t *testing.T) {
	runLexCases(t, "java", map[string]lexCase{
		"a text block holds quotes and comment markers": {
			"String s = \"\"\"\n  a \"q\" // not a comment\n  \"\"\";\nint x = 'y'; // it's\n",
			"String s = \"\"\"\n                        \n  \"\"\";\nint x = ' ';        \n",
		},
		"a double quote as a char":  {`char c = '"'; String t = "a";`, `char c = ' '; String t = " ";`},
		"a string ends at its line": {"String s = \"open\nint x = 'y';\n", "String s = \"    \nint x = ' ';\n"},
		"an escaped quote":          {`String s = "a\"b"; // c`, `String s = "    ";     `},
		"a block comment":           {"int a; /* it's */ int b = 'c';", "int a;            int b = ' ';"},
	})
}

func TestLex_KotlinNestedCommentsAndRawStrings(t *testing.T) {
	runLexCases(t, "kotlin", map[string]lexCase{
		"a nested block comment ends at its outer closer": {"/* a /* b */ c */ x", "                  x"},
		"a raw string holds quotes and a backslash":       {`val s = """a "b" \ c""" + "d"`, `val s = """         """ + " "`},
		"a line comment with an apostrophe":               {"val a = 1 // it's\nval b = 'c'", "val a = 1        \nval b = ' '"},
		"a string ends at its line":                       {"val s = \"open\nval c = 'x'", "val s = \"    \nval c = ' '"},
	})
}

func TestLex_CSharpVerbatimAndRawStrings(t *testing.T) {
	runLexCases(t, "csharp", map[string]lexCase{
		"a verbatim string doubles its quote and keeps a backslash": {`var s = @"a ""q"" \"; var t = "b";`, `var s = @"         "; var t = " ";`},
		"a verbatim string spans lines":                             {"var s = @\"a\nb\"; var c = 'x';", "var s = @\" \n \"; var c = ' ';"},
		"a raw string":                                              {`var s = """a "b" c"""; // x`, `var s = """       """;     `},
		"an escaped quote in a plain string":                        {`var s = "a\"b"; // c`, `var s = "    ";     `},
	})
}

func TestLex_PHPHashAndSlashComments(t *testing.T) {
	runLexCases(t, "php", map[string]lexCase{
		"a hash comment":               {"$a = 1; # it's\n$b = 'c';", "$a = 1;       \n$b = ' ';"},
		"a slash comment":              {"$a = 1; // it's\n$b = 'c';", "$a = 1;        \n$b = ' ';"},
		"a block comment":              {"/* it's */ $b = 'c';", "           $b = ' ';"},
		"an escaped quote":             {`$s = 'it\'s'; # c`, `$s = '     ';    `},
		"a single quoted string spans": {"$s = 'a\nb'; $t = \"c\";", "$s = ' \n '; $t = \" \";"},
	})
}

// Block comments nest only where the row says so.
func TestLex_BlockCommentsNestOnlyWhenTheRowSaysSo(t *testing.T) {
	plain := synth(t, "name = \"p\"\n[comments]\nblock = [\"{- -}\"]\n")
	nested := synth(t, "name = \"n\"\n[comments]\nblock = [\"{- -}\"]\nblock_nested = true\n")
	src := "{- a {- b -} c -} x"
	if got, want := plain.Lex(src, false, true), "             c -} x"; got != want {
		t.Errorf("not nested:\n got %q\nwant %q", got, want)
	}
	if got, want := nested.Lex(src, false, true), "                  x"; got != want {
		t.Errorf("nested:\n got %q\nwant %q", got, want)
	}
	if got, want := nested.Lex("{- a {- b -} c x", false, true), "                "; got != want {
		t.Errorf("an unbalanced nested comment runs to the end:\n got %q\nwant %q", got, want)
	}
}

func TestLex_MultiByteMarkersAreBlankedWhole(t *testing.T) {
	x := synth(t, "name = \"m\"\n[comments]\nline = [\"--\", \"%%\"]\nblock = [\"(* *)\"]\n")
	cases := map[string]lexCase{
		"a two byte line marker":  {"a -- b\nc", "a     \nc"},
		"a second line marker":    {"a %% b\nc", "a     \nc"},
		"a one byte is no marker": {"a - b % c", "a - b % c"},
		"a block":                 {"a (* b *) c", "a         c"},
		"a lone opener byte":      {"a ( b * ) c", "a ( b * ) c"},
	}
	for name, c := range cases {
		if got := x.Lex(c.src, false, true); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

func TestLex_DoublingStringWithAMultiByteCloser(t *testing.T) {
	x := synth(t, "name = \"d\"\n[string.s]\nopen = \"<<\"\nclose = \">>\"\nescape = \"doubling\"\nmultiline = true\n")
	if got, want := x.Lex("a <<x >>>> y>> z", true, false), "a <<        >> z"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got, want := x.Lex("a <<x >> y >> z", true, false), "a <<  >> y >> z"; got != want {
		t.Errorf("a single closer ends the string:\n got %q\nwant %q", got, want)
	}
}

func TestLex_OnlyTheRequestedCategoryIsBlanked(t *testing.T) {
	src := "a = 'x' # it's\n"
	if got, want := lexAs(t, "python", src, true, false), "a = ' ' # it's\n"; got != want {
		t.Errorf("strings only:\n got %q\nwant %q", got, want)
	}
	if got, want := lexAs(t, "python", src, false, true), "a = 'x'       \n"; got != want {
		t.Errorf("comments only:\n got %q\nwant %q", got, want)
	}
	if got := lexAs(t, "python", src, false, false); got != src {
		t.Errorf("neither: got %q", got)
	}
}

func TestLex_AStringFormOpensOnlyWhereItsRowSaysSo(t *testing.T) {
	line := synth(t, "name = \"l\"\n[string.s]\nopen = \"@@\"\nclose = \"\\n\"\nmultiline = true\nline_start = true\nblank_open = true\n")
	if got, want := line.Lex("  @@ab 'c'\nx @@y\n", true, false), "          \nx @@y\n"; got != want {
		t.Errorf("a line-start form:\n got %q\nwant %q", got, want)
	}
	after := synth(t, "name = \"a\"\n[string.s]\nopen = \"'\"\nopens_after = \"=\"\n")
	if got, want := after.Lex("k = 'v' don't 'w'\n", true, false), "k = ' ' don't 'w'\n"; got != want {
		t.Errorf("an after-set form:\n got %q\nwant %q", got, want)
	}
}

func TestTokens_DefaultRowsThroughTheOldEntryPoints(t *testing.T) {
	if got, want := StringsAndComments("a = 'x' # c // d\nb /* e */ `f`"), "a = ' '         \nb         ` `"; got != want {
		t.Errorf("StringsAndComments:\n got %q\nwant %q", got, want)
	}
	if got, want := Tokens("a # 'b'\n", true, false, false), "a # 'b'\n"; got == want {
		t.Errorf("without hashComment the quote in a # line is a string: got %q", got)
	}
	if got, want := Tokens("a # 'b'\n", true, false, true), "a # 'b'\n"; got != want {
		t.Errorf("with hashComment the quote in a # line opens nothing: got %q", got)
	}
}

func TestForFile_PicksTheRowAndTheViewThatReadsAFile(t *testing.T) {
	tbl := defaults(t)
	src := "x = 'a' # it's\ny = 'b'\n"
	cases := []struct {
		file string
		view int
		want string
	}{
		{"a.py", 0, "x = ' ' # it's\ny = ' '\n"},
		{"a.py", 1, "x = ' ' # it' \n    'b'\n"},
		{"a.unknown", 0, "x = ' ' # it' \n    'b'\n"},
	}
	for _, c := range cases {
		if got := ForFile(tbl, c.file, c.view).Lex(src, true, false); got != c.want {
			t.Errorf("ForFile(%q, %d):\n got %q\nwant %q", c.file, c.view, got, c.want)
		}
	}
	slash := "a // it's\nb = 'c'"
	if got, want := CommentsForFile(tbl, "a.java", 0).Lex(slash, false, true), "a        \nb = 'c'"; got != want {
		t.Errorf("CommentsForFile java: got %q want %q", got, want)
	}
	if got := CommentsForFile(tbl, "a.py", 0).Lex("a # c\nb", false, true); got != "a # c\nb" {
		t.Errorf("a python file has no // comment and keeps the default row: got %q", got)
	}
}
