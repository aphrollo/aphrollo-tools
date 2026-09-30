package lang

import (
	"regexp"
	"strings"
	"testing"
)

func TestParse_DeclarationsAreLinePatternsNeedingNoCaptureGroup(t *testing.T) {
	l, err := Parse("name = \"x\"\nextensions = [\".x\"]\n[tests]\ndeclarations = ['^\\s*spec\\b', '(a)(b)']\n", "x.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Declarations) != 2 || len(l.Tests) != 0 {
		t.Fatalf("declarations = %d, tests = %d, want 2 and 0", len(l.Declarations), len(l.Tests))
	}
	if !l.Declarations[0].MatchString("  spec foo") || l.Declarations[0].MatchString("x spec") {
		t.Error("the first declaration pattern was not compiled as written")
	}
}

func TestParse_ADeclarationThatIsNotARegexIsRefused(t *testing.T) {
	_, err := Parse("name = \"x\"\n[tests]\ndeclarations = [\"(\"]\n", "x.toml")
	if err == nil || !strings.Contains(err.Error(), "x.toml: [tests] declaration \"(\" is not a regular expression") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeclaresTest_AnswersPerExtensionAndSaysWhenTheTableDoesNotKnow(t *testing.T) {
	row := mustRow(t, "name = \"x\"\nextensions = [\".x\", \".y\"]\n[tests]\ndeclarations = ['^spec ']\n")
	bare := mustRow(t, "name = \"z\"\nextensions = [\".z\"]\n")
	tbl, err := (&Table{}).Extend([]Language{row, bare})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ext, line   string
		decl, known bool
	}{
		{".x", "spec a", true, true},
		{".y", "spec a", true, true},
		{".x", " spec a", false, true},
		{".z", "spec a", false, false},
		{".w", "spec a", false, false},
		{"", "spec a", false, false},
	}
	for _, c := range cases {
		decl, known := tbl.DeclaresTest(c.ext, c.line)
		if decl != c.decl || known != c.known {
			t.Errorf("DeclaresTest(%q, %q) = (%v, %v), want (%v, %v)", c.ext, c.line, decl, known, c.decl, c.known)
		}
	}
}

func TestDeclaresTest_ALineIsJudgedByEveryPatternOfTheRow(t *testing.T) {
	row := mustRow(t, "name = \"x\"\nextensions = [\".x\"]\n[tests]\ndeclarations = ['^one', '^two']\n")
	tbl, err := (&Table{}).Extend([]Language{row})
	if err != nil {
		t.Fatal(err)
	}
	for line, want := range map[string]bool{"one": true, "two": true, "three": false} {
		if decl, known := tbl.DeclaresTest(".x", line); decl != want || !known {
			t.Errorf("DeclaresTest(%q) = (%v, %v), want (%v, true)", line, decl, known, want)
		}
	}
}

func TestTestNames_ListsTheNamesEachPatternCapturesInOrder(t *testing.T) {
	row := mustRow(t, "name = \"x\"\n[tests]\npatterns = ['^test (\\w+)', '^spec (\\w+)']\n")
	src := "test b\nnot a test\nspec z\ntest a\n  test indented\nspec y\n"
	got := strings.Join(row.TestNames(src), ",")
	if got != "b,a,z,y" {
		t.Errorf("TestNames = %s, want b,a,z,y", got)
	}
	if names := (Language{}).TestNames(src); len(names) != 0 {
		t.Errorf("a row with no test pattern names %v", names)
	}
}

func TestSelectableNames_ReadsSelectablePatternsElseTheTestPatterns(t *testing.T) {
	strict := mustRow(t, "name = \"x\"\n[tests]\npatterns = ['^test (\\w+)\\(']\nselectable = ['^test\\s+(\\w+)\\s*\\(']\n")
	src := "test  a (\ntest b(\n"
	if got := strings.Join(strict.TestNames(src), ","); got != "b" {
		t.Errorf("TestNames = %s, want b: the test patterns stay as written", got)
	}
	if got := strings.Join(strict.SelectableNames(src), ","); got != "a,b" {
		t.Errorf("SelectableNames = %s, want a,b", got)
	}
	plain := mustRow(t, "name = \"y\"\n[tests]\npatterns = ['^test (\\w+)\\(']\n")
	if got := strings.Join(plain.SelectableNames(src), ","); got != "b" {
		t.Errorf("a row with no selectable pattern names by its test patterns, got %s", got)
	}
}

func TestParse_ASelectablePatternNeedsExactlyOneGroup(t *testing.T) {
	for name, pattern := range map[string]string{"none": "a", "two": "(a)(b)", "broken": "("} {
		if _, err := Parse("name = \"x\"\n[tests]\nselectable = ['"+pattern+"']\n", "x.toml"); err == nil ||
			!strings.Contains(err.Error(), "x.toml: [tests] selectable pattern") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := Parse("name = \"x\"\n[tests]\nselectable = ['(a)']\n", "x.toml"); err != nil {
		t.Errorf("one group is the required count: %v", err)
	}
}

func TestWholeFile_AnchorsBindToEachLineUnlessThePatternChoseItsOwnFlags(t *testing.T) {
	plain := WholeFile(regexp.MustCompile(`^x$`))
	if !plain.MatchString("a\nx\nb") {
		t.Error("a pattern without flags must bind to a line of the file, not the file")
	}
	own := regexp.MustCompile(`(?s)^x$`)
	if WholeFile(own) != own {
		t.Error("a pattern that opens with its own flag group is trusted as written")
	}
	if WholeFile(own).MatchString("a\nx\nb") {
		t.Error("a pattern with its own flags must keep them")
	}
}
