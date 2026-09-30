package lang

import (
	"testing"
)

func TestLexRow_AFileIsReadByItsRowOnlyOnceTheRowTookEffect(t *testing.T) {
	tbl, _ := Defaults()
	cases := []struct {
		file string
		view int
		want string
	}{
		{"a.py", 0, "python"},
		{"a.py", 3, "python"},
		{"a.py", 2, "python"},
		{"a.py", 1, Neutral},
		{"a.rs", 1, "rust"},
		{"a.rs", 0, "rust"},
		{"A.JAVA", 3, "java"},
		{"A.JAVA", 2, Neutral},
		{"A.JAVA", 0, "java"},
		{"a.go", 0, Neutral},
		{"a.ts", 3, Neutral},
		{"unknown.xyz", 0, Neutral},
		{"Makefile", 0, Neutral},
	}
	for _, c := range cases {
		if got := tbl.LexRow(c.file, c.view).Name; got != c.want {
			t.Errorf("LexRow(%q, %d) = %s, want %s", c.file, c.view, got, c.want)
		}
	}
}

func TestCommentRow_OnlyARowWithSlashCommentsBlanksForASlashLaw(t *testing.T) {
	tbl, _ := Defaults()
	cases := []struct {
		file string
		view int
		want string
	}{
		{"a.rs", 0, "rust"},
		{"a.rs", 1, "rust"},
		{"a.py", 0, Neutral},
		{"a.sh", 0, Neutral},
		{"a.java", 0, "java"},
		{"a.java", 3, "java"},
		{"a.java", 2, Neutral},
		{"a.php", 0, "php"},
		{"a.go", 0, Neutral},
	}
	for _, c := range cases {
		if got := tbl.CommentRow(c.file, c.view).Name; got != c.want {
			t.Errorf("CommentRow(%q, %d) = %s, want %s", c.file, c.view, got, c.want)
		}
	}
}

func TestViewFor_IsTheLatestViewOfTheRowsALawTouches(t *testing.T) {
	tbl, _ := Defaults()
	only := func(exts ...string) func(string) bool {
		return func(ext string) bool {
			for _, e := range exts {
				if e == ext {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		name  string
		exts  []string
		slash bool
		want  int
	}{
		{"nothing touched", nil, false, 1},
		{"rust has always lexed as it does", []string{".rs"}, false, 1},
		{"python", []string{".py"}, false, 2},
		{"python and rust", []string{".py", ".rs"}, false, 2},
		{"java", []string{".java"}, false, 3},
		{"python and java", []string{".py", ".java"}, false, 3},
		{"yaml", []string{".yml"}, false, 2},
		{"go lexes as the default row", []string{".go"}, false, 1},
		{"python is no slash-comment row", []string{".py"}, true, 1},
		{"java is a slash-comment row", []string{".java"}, true, 3},
		{"php is a slash-comment row", []string{".php"}, true, 4},
		{"php is the latest row a scope of php and java reaches", []string{".php", ".java"}, false, 4},
		{"python and java, slash only", []string{".py", ".java"}, true, 3},
	}
	for _, c := range cases {
		if got := tbl.ViewFor(only(c.exts...), c.slash); got != c.want {
			t.Errorf("%s: ViewFor = %d, want %d", c.name, got, c.want)
		}
	}
	later, err := tbl.Extend([]Language{mustRow(t, "name = \"zz\"\nextensions = [\".zz\"]\nview = 9\n[comments]\nline = [\"//\"]\n")})
	if err != nil {
		t.Fatal(err)
	}
	if got := later.ViewFor(only(".zz"), false); got != 9 {
		t.Errorf("a repo row at view 9: ViewFor = %d, want 9", got)
	}
}

func TestLexes_ARowLexesWhenItDeclaresAnyLexing(t *testing.T) {
	cases := map[string]bool{
		"name = \"a\"\n":                                                 false,
		"name = \"a\"\ncode_escape = true\n":                             true,
		"name = \"a\"\n[comments]\nline = [\"#\"]\n":                     true,
		"name = \"a\"\n[comments]\nblock = [\"a b\"]\n":                  true,
		"name = \"a\"\n[string.s]\nopen = \"'\"\n":                       true,
		"name = \"a\"\n[suppress.s]\nkind = \"lint\"\npattern = \"a\"\n": false,
	}
	for text, want := range cases {
		if got := mustRow(t, text).Lexes(); got != want {
			t.Errorf("%q Lexes() = %v, want %v", text, got, want)
		}
	}
}
