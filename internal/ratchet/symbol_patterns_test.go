package ratchet

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// lawOver is a symbol-removed law with no pattern of its own over the globs.
func lawOver(t *testing.T, globs ...string) Law {
	t.Helper()
	quoted := make([]string, len(globs))
	for i, g := range globs {
		quoted[i] = `"` + g + `"`
	}
	law, err := ParseLaw("name = \"test_removed\"\ndescription = \"x\"\nseverity = \"deny\"\n"+
		"[scope]\ninclude = ["+strings.Join(quoted, ", ")+"]\n[matcher]\nkind = \"symbol-removed\"\n", "test_removed")
	if err != nil {
		t.Fatalf("a symbol-removed law may omit its pattern: %v", err)
	}
	return law
}

func TestSymbolPatterns_AnOmittedPatternIsTheTestPatternsOfTheRowsTheScopeNames(t *testing.T) {
	cases := []struct {
		name   string
		globs  []string
		src    string
		want   string
		absent bool
	}{
		{"go", []string{"**/*_test.go"}, "func TestFoo(t *testing.T) {}\n", "TestFoo", false},
		{"go skips TestMain", []string{"**/*_test.go"}, "func TestMain(m *testing.M) {}\n", "", true},
		{"rust", []string{"**/*.rs"}, "#[test]\nfn adds() {}\n", "adds", false},
		{"python", []string{"**/*.py"}, "class T:\n    def test_adds(self):\n        pass\n", "test_adds", false},
		{"python helper", []string{"**/*.py"}, "def helper():\n    pass\n", "", true},
		{"javascript", []string{"**/*.js"}, "it('adds two', () => {})\n", "adds two", false},
		{"typescript double quotes", []string{"**/*.ts"}, "test(\"adds two\", () => {})\n", "adds two", false},
		{"java", []string{"**/*.java"}, "@Test\npublic void adds() {}\n", "adds", false},
		{"java with a modifier", []string{"**/*.java"}, "@Test\n@Disabled\nstatic void skipped() {}\n", "skipped", false},
		{"csharp", []string{"**/*.cs"}, "[Fact]\npublic void Adds() {}\n", "Adds", false},
		{"csharp theory", []string{"**/*.cs"}, "[Theory]\n[InlineData(1)]\npublic async Task AddsAsync(int x) {}\n", "AddsAsync", false},
		{"kotlin", []string{"**/*.kt"}, "@Test\nfun adds() {}\n", "adds", false},
		{"kotlin backticked", []string{"**/*.kt"}, "@Test\nfun `adds two numbers`() {}\n", "adds two numbers", false},
		{"php", []string{"**/*.php"}, "public function testAdds() {}\n", "testAdds", false},
		{"several rows", []string{"**/*.py", "**/*.rs"}, "#[test]\nfn adds() {}\n", "adds", false},
	}
	for _, c := range cases {
		patterns, err := lawOver(t, c.globs...).symbolPatterns()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := symbolNames(patterns, c.src)
		if c.absent {
			if len(got) != 0 {
				t.Errorf("%s: captured %q, want nothing", c.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: captured %q, want [%s]", c.name, got, c.want)
		}
	}
}

func TestSymbolPatterns_AStatedPatternWinsAndIsLeftAsWritten(t *testing.T) {
	law, err := ParseLaw("name = \"x\"\ndescription = \"x\"\nseverity = \"deny\"\n[scope]\ninclude = [\"**/*.py\"]\n"+
		"[matcher]\nkind = \"symbol-removed\"\npattern = \"^check_(\\\\w+)\"\n", "x")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := law.SymbolPatterns()
	if err != nil || len(raw) != 1 || raw[0].String() != `^check_(\w+)` {
		t.Fatalf("SymbolPatterns = %v, %v, want the stated pattern verbatim", raw, err)
	}
	whole, _ := law.symbolPatterns()
	if got := symbolNames(whole, "check_a\nx\ncheck_b\n"); strings.Join(got, ",") != "a,b" {
		t.Errorf("captured %q, want a,b per line", got)
	}
}

func TestSymbolPatterns_AScopeNamingNoTestRowIsAnError(t *testing.T) {
	for _, globs := range [][]string{{"**/*.xyz"}, {"**/*.css"}, {"**/*.toml"}, {"**/Makefile"}} {
		_, err := lawOver(t, globs...).symbolPatterns()
		if err == nil || !strings.Contains(err.Error(), `law "test_removed" states no matcher.pattern`) {
			t.Errorf("globs %v: err = %v, want the law to be refused for naming no test row", globs, err)
		}
	}
}

func TestSymbolPatterns_ARepositoryRowSuppliesItsOwnTestPattern(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), "name = \"lua\"\nextensions = [\".lua\"]\nfilenames = [\"spec\"]\n[tests]\npatterns = ['(?m)^describe\\(\"([^\"]+)\"']\n")
	law := lawOver(t, "**/*.lua")
	law.Root = root
	patterns, err := law.symbolPatterns()
	if err != nil {
		t.Fatal(err)
	}
	if got := symbolNames(patterns, "describe(\"adds\", function() end)\n"); len(got) != 1 || got[0] != "adds" {
		t.Errorf("captured %q, want [adds]", got)
	}
	byName := lawOver(t, "sub/spec")
	byName.Root = root
	if _, err := byName.symbolPatterns(); err != nil {
		t.Errorf("a glob ending in a row's file name names the row: %v", err)
	}
	other := lawOver(t, "sub/notspec")
	other.Root = root
	if _, err := other.symbolPatterns(); err == nil {
		t.Error("a glob whose last segment merely ends with a file name names no row")
	}
}

func TestScopeNamesRow_IsCaseInsensitiveAndExact(t *testing.T) {
	tbl, _ := lang.Defaults()
	java, _ := tbl.Named("java")
	cases := map[string]bool{
		"**/*.java":       true,
		"**/*.JAVA":       true,
		"src/**/Foo.java": true,
		"**/*.javascript": false,
		"**/*.jav":        false,
		"**/*.class":      false,
	}
	for glob, want := range cases {
		law := Law{}
		law.Scope.Include = []string{glob}
		if got := law.scopeNamesRow(java); got != want {
			t.Errorf("scopeNamesRow(%q) = %v, want %v", glob, got, want)
		}
	}
	if (Law{}).scopeNamesRow(java) {
		t.Error("a law with no include names no row")
	}
}

func TestSymbolNames_InOrderOfAppearanceWithinEachPattern(t *testing.T) {
	law := lawOver(t, "**/*.py", "**/*.rs")
	patterns, _ := law.symbolPatterns()
	src := "def test_a():\n  pass\n#[test]\nfn b() {}\ndef test_c():\n  pass\n"
	got := symbolNames(patterns, src)
	sort.Strings(got)
	if strings.Join(got, ",") != "b,test_a,test_c" {
		t.Errorf("names = %v", got)
	}
}

func TestSymbolRemoved_TheGoAndRustRowsEqualTheirPresets(t *testing.T) {
	tbl, _ := lang.Defaults()
	for group, row := range map[string]string{"go": "go", "rust": "rust"} {
		data, err := presetsFS.ReadFile(presetsRoot + "/" + group + "/test_removed.toml")
		if err != nil {
			t.Fatal(err)
		}
		preset, err := ParseLaw(string(data), "test_removed")
		if err != nil {
			t.Fatal(err)
		}
		r, _ := tbl.Named(row)
		if len(r.Tests) != 1 || r.Tests[0].String() != preset.Matcher.Pattern.String() {
			t.Errorf("%s: the row's test pattern %v differs from the preset's %s — one of them moved", group, r.Tests, preset.Matcher.Pattern)
		}
	}
}

func TestSymbolRemoved_AnOmittedPatternJudgesEveryLanguageByItsRow(t *testing.T) {
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "test_removed", `
name = "test_removed"
description = "A test's disappearance from a diff needs a tombstone, not silence"
severity = "deny"

[scope]
include = ["**/*_test.go", "**/*.py", "**/*.rs", "**/*.java", "**/*.kt", "**/*.cs", "**/*.php"]

[matcher]
kind = "symbol-removed"
`)
	files := map[string][2]string{
		"a_test.go": {"package a\nfunc TestKeep(t *testing.T) {}\nfunc TestGoGone(t *testing.T) {}\n", "package a\nfunc TestKeep(t *testing.T) {}\n"},
		"t.py":      {"def test_keep():\n    pass\ndef test_py_gone():\n    pass\n", "def test_keep():\n    pass\n"},
		"t.rs":      {"#[test]\nfn keep() {}\n#[test]\nfn rs_gone() {}\n", "#[test]\nfn keep() {}\n"},
		"T.java":    {"@Test\nvoid keep() {}\n@Test\nvoid javaGone() {}\n", "@Test\nvoid keep() {}\n"},
		"T.kt":      {"@Test\nfun keep() {}\n@Test\nfun ktGone() {}\n", "@Test\nfun keep() {}\n"},
		"T.cs":      {"[Fact]\npublic void Keep() {}\n[Fact]\npublic void CsGone() {}\n", "[Fact]\npublic void Keep() {}\n"},
		"T.php":     {"function testKeep() {}\nfunction testPhpGone() {}\n", "function testKeep() {}\n"},
	}
	for name, v := range files {
		write(t, filepath.Join(root, name), v[0])
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")
	for name, v := range files {
		write(t, filepath.Join(root, name), v[1])
	}
	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Key)
	}
	sort.Strings(got)
	want := "T.cs:CsGone T.java:javaGone T.kt:ktGone T.php:testPhpGone a_test.go:TestGoGone t.py:test_py_gone t.rs:rs_gone"
	if strings.Join(got, " ") != want {
		t.Errorf("findings = %v\nwant    %s", got, want)
	}
}

func TestCheck_ASymbolRemovedLawNamingNoTestRowIsAnErrorNotAPass(t *testing.T) {
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "test_removed", "name = \"test_removed\"\ndescription = \"x\"\nseverity = \"deny\"\n[scope]\ninclude = [\"**/*.xyz\"]\n[matcher]\nkind = \"symbol-removed\"\n")
	write(t, filepath.Join(root, "a.xyz"), "x\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")
	if _, err := Check(Options{Root: root, Base: "HEAD"}); err == nil || !strings.Contains(err.Error(), "states no matcher.pattern") {
		t.Errorf("err = %v, want the law refused for capturing nothing", err)
	}
}
