package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

const secretJavaLaw = `
name         = "secret_java"
description  = "a secret stays out of code"
severity     = "deny"
baseline     = ".ratchet/baselines/secret_java.txt"
mask_strings = true

[scope]
include = ["**/*.java"]

[matcher]
kind    = "regex-absent"
pattern = "SECRET"
key     = "file:line-content-hash"
`

// secretTextBlock holds SECRET inside a Java text block. The default lexer
// reads each quote of the block as opening or closing a string, and three
// quotes inside it leave SECRET outside one; the Java row reads the whole block
// as one string, so SECRET is masked.
const secretTextBlock = "class A {\n  String s = \"\"\"\n      a \"b\" \"c SECRET\n      \"\"\";\n}\n"

func javaRepo(t *testing.T, baseline string) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "secret_java", secretJavaLaw)
	write(t, filepath.Join(root, "app", "A.java"), secretTextBlock)
	write(t, filepath.Join(root, ".ratchet", "baselines", "secret_java.txt"), baseline)
	return root
}

func readBaseline(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCheck_ABaselineWrittenBeforeTheJavaRowIsJudgedByTheDefaultLexer(t *testing.T) {
	root := javaRepo(t, "app/A.java | a \"b\" \"c SECRET\n")
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none: the baseline records what the default lexer saw", res.Findings)
	}
	if n := notesContaining(res, "secret_java: its baseline is stamped below `# scan-view: 3`"); len(n) != 1 {
		t.Errorf("notes = %q, want the legacy note naming the view 3 stamp", res.Notes)
	}
}

func TestCheck_AStampedBaselineIsJudgedByTheJavaRow(t *testing.T) {
	root := javaRepo(t, "# scan-view: 3\n")
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || len(res.Notes) != 0 {
		t.Fatalf("findings=%+v notes=%q, want green and silent: the Java row masks the text block", res.Findings, res.Notes)
	}
}

func TestCheck_AJavaBaselineMigratesToViewThreeOnTheFirstTighteningCheck(t *testing.T) {
	for name, baseline := range map[string]string{
		"unstamped":           "app/A.java | a \"b\" \"c SECRET\n",
		"stamped at 2":        ScanViewStamp + "\napp/A.java | a \"b\" \"c SECRET\n",
		"stamped at 1":        "# scan-view: 1\napp/A.java | a \"b\" \"c SECRET\n",
		"stamp in the middle": "# note\n# scan-view: 2\napp/A.java | a \"b\" \"c SECRET\n",
	} {
		root := javaRepo(t, baseline)
		res, err := Check(Options{Root: root, Tighten: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(res.Findings) != 0 {
			t.Fatalf("%s: findings = %+v, want none on the migrating run", name, res.Findings)
		}
		got := readBaseline(t, root, "secret_java")
		if strings.Contains(got, "SECRET") || strings.Count(got, "# scan-view:") != 1 || !strings.Contains(got, "# scan-view: 3\n") {
			t.Errorf("%s: baseline = %q, want one stamp at view 3 and no row for the masked text block", name, got)
		}
		if n := notesContaining(res, "migrated secret_java to the current lexers (0 rows)"); len(n) != 1 {
			t.Errorf("%s: notes = %q, want the migration line", name, res.Notes)
		}
		again, err := Check(Options{Root: root, Tighten: true})
		if err != nil || len(again.Findings) != 0 || len(again.Notes) != 0 {
			t.Errorf("%s: second run = %+v %v, want green and silent", name, again, err)
		}
	}
}

func TestCheck_AJavaTreeAboveItsLegacyBaselineIsNotMigrated(t *testing.T) {
	root := javaRepo(t, "")
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "app/A.java" {
		t.Fatalf("findings = %+v, want the one hit the default lexer reads over a ceiling of zero", res.Findings)
	}
	if got := readBaseline(t, root, "secret_java"); got != "" {
		t.Errorf("baseline = %q, want it untouched", got)
	}
}

func TestHitsIn_AStampedViewReadsEachFileKindByTheRowsOfThatView(t *testing.T) {
	law, err := ParseLaw(`
name         = "secret_any"
description  = "x"
severity     = "deny"
mask_strings = true

[scope]
include = ["**/*.java", "**/*.py"]

[matcher]
kind    = "regex-absent"
pattern = "SECRET"
key     = "file:line-content-hash"
`, "secret_any")
	if err != nil {
		t.Fatal(err)
	}
	// In Python a `#` comment opens no string; the default lexer reads the
	// apostrophe of "it's" as a quote and blanks the line below.
	py := "x = 1  # it's\nSECRET = 'a'\n"
	cases := []struct {
		name string
		law  Law
		file string
		src  string
		want int
	}{
		{"current view, python row", law, "a.py", py, 1},
		{"view 3, python row", withView(law, 3), "a.py", py, 1},
		{"view 2, python row", withView(law, 2), "a.py", py, 1},
		{"view 1, default lexer", withView(law, 1), "a.py", py, 0},
		{"legacy with no version is view 1", func() Law { l := law; l.LegacyView = true; return l }(), "a.py", py, 0},
		{"current view, java row", law, "A.java", secretTextBlock, 0},
		{"view 3, java row", withView(law, 3), "A.java", secretTextBlock, 0},
		{"view 2, default lexer", withView(law, 2), "A.java", secretTextBlock, 1},
		{"view 1, default lexer", withView(law, 1), "A.java", secretTextBlock, 1},
	}
	for _, c := range cases {
		if got := len(c.law.HitsIn(c.file, c.src)); got != c.want {
			t.Errorf("%s: %d hits, want %d", c.name, got, c.want)
		}
	}
}

func withView(l Law, view int) Law {
	l.LegacyView, l.ViewVersion = true, view
	return l
}

func TestTouchedView_ALawIsSensitiveToTheRowsItsScopeNames(t *testing.T) {
	mk := func(flags, include string) Law {
		l, err := ParseLaw("name = \"t\"\ndescription = \"x\"\nseverity = \"deny\"\n"+flags+
			"[scope]\ninclude = ["+include+"]\n[matcher]\nkind = \"regex-absent\"\npattern = \"x\"\nkey = \"file:line-content-hash\"\n", "t")
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	cases := []struct {
		name  string
		law   Law
		want  int
		exact bool
	}{
		{"mask strings over python", mk("mask_strings = true\n", `"**/*.py"`), 2, true},
		{"mask strings over java", mk("mask_strings = true\n", `"**/*.java"`), 3, true},
		{"mask strings over java and python", mk("mask_strings = true\n", `"**/*.py", "**/*.java"`), 3, true},
		{"mask strings over rust", mk("mask_strings = true\n", `"**/*.rs"`), 1, true},
		{"mask strings over go", mk("mask_strings = true\n", `"**/*.go"`), 1, true},
		{"mask strings over css is not csharp", mk("mask_strings = true\n", `"**/*.css"`), 1, true},
		{"mask strings over csharp", mk("mask_strings = true\n", `"**/*.cs"`), 3, true},
		{"no lexing view", mk("", `"**/*.java"`), 1, true},
		{"code only with the slash comment over java", mk("code_only = true\n", `"**/*.java"`), 3, true},
		{"code only with the slash comment over python", mk("code_only = true\n", `"**/*.py"`), 1, true},
		{"code only with a hash comment over java", mk("code_only = true\ncomment_prefix = \"#\"\n", `"**/*.java"`), 1, true},
		{"code only over python with a hash comment", mk("code_only = true\ncomment_prefix = \"#\"\n", `"**/*.py"`), 1, true},
		{"mask strings and code only over python", mk("code_only = true\nmask_strings = true\ncomment_prefix = \"#\"\n", `"**/*.py"`), 2, true},
		{"upper case glob", mk("mask_strings = true\n", `"**/*.JAVA"`), 3, true},
	}
	for _, c := range cases {
		if got := c.law.touchedView(); got != c.want {
			t.Errorf("%s: touchedView = %d, want %d", c.name, got, c.want)
		}
		if got, want := c.law.viewSensitive(), c.want > 1; got != want {
			t.Errorf("%s: viewSensitive = %v, want %v", c.name, got, want)
		}
	}
}

func TestGlobNamesExt_TheExtensionStandsAtTheGlobsEndOrBeforeANonExtensionByte(t *testing.T) {
	cases := []struct {
		glob string
		want bool
	}{
		{"**/*.cs", true},
		{"**/*.cs/**", true},
		{"**/*.css", false},
		{"**/*.cs2", false},
		{"**/*.cs_x", false},
		{"**/*.cs-x", false},
		{"**/*.CS", false},
		{"**/x.csx.cs", true},
		{"**/x.csx.csy", false},
		{"**/*.c", false},
		{"", false},
		{".cs", true},
		// The bytes either side of each range an extension holds: a, z, 0, 9.
		{"**/*.csa", false},
		{"**/*.csz", false},
		{"**/*.cs0", false},
		{"**/*.cs9", false},
		{"**/*.cs`", true},
		{"**/*.cs{", true},
		{"**/*.cs/", true},
		{"**/*.cs:", true},
		{"**/*.cs@", true},
	}
	for _, c := range cases {
		if got := globNamesExt(c.glob, ".cs"); got != c.want {
			t.Errorf("globNamesExt(%q, .cs) = %v, want %v", c.glob, got, c.want)
		}
	}
}

func TestScanViewOf_ReadsTheHighestStampAndDefaultsToOne(t *testing.T) {
	cases := map[string]int{
		"":                               1,
		"row\n":                          1,
		"# scan-view: 2\nrow\n":          2,
		"# scan-view: 3\nrow\n":          3,
		"# scan-view:4\n":                4,
		"  # scan-view: 5  \n":           5,
		"# scan-view: 2\n# scan-view: 4": 4,
		"# scan-view: 4\n# scan-view: 2": 4,
		"# scan-view: x\n":               1,
		"# scan-view: 0\n":               1,
		"# scan-view: 1\n":               1,
		"row | # scan-view: 9\n":         1,
	}
	for text, want := range cases {
		if got := ScanViewOf(text); got != want {
			t.Errorf("ScanViewOf(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestBaselineStampAt_RaisesAStampInPlaceAndNeverLowersOne(t *testing.T) {
	cases := []struct {
		name, text string
		view       int
		want       string
	}{
		{"none", "# note\nrow one\n", 3, "# scan-view: 3\n# note\nrow one\n"},
		{"lower", "# scan-view: 2\n# note\nrow one\n", 3, "# scan-view: 3\n# note\nrow one\n"},
		{"lower mid file", "# note\n# scan-view: 2\nrow one\n", 3, "# note\n# scan-view: 3\nrow one\n"},
		{"equal", "# scan-view: 3\nrow one\n", 3, "# scan-view: 3\nrow one\n"},
		{"higher", "# scan-view: 4\nrow one\n", 3, "# scan-view: 4\nrow one\n"},
	}
	for _, c := range cases {
		b, err := ParseBaseline(c.text, Multiset)
		if err != nil {
			t.Fatal(err)
		}
		b.StampAt(c.view)
		if got := b.Render(); got != c.want {
			t.Errorf("%s: Render = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLanguagesFingerprint_FollowsTheRepositoryRows(t *testing.T) {
	root := t.TempDir()
	bare := languagesFingerprint(root)
	if !strings.HasPrefix(bare, "+") || len(bare) != 17 {
		t.Fatalf("fingerprint = %q, want a + and 16 hex digits", bare)
	}
	if other := languagesFingerprint(t.TempDir()); other != bare {
		t.Errorf("two repositories with no rows of their own differ: %q and %q — the key is the embedded table's", bare, other)
	}
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), "name = \"lua\"\nextensions = [\".lua\"]\n")
	a := languagesFingerprint(root)
	if a == bare {
		t.Fatal("a repository row left the fingerprint at the embedded table's")
	}
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), "name = \"lua\"\nextensions = [\".lua\", \".luau\"]\n")
	b := languagesFingerprint(root)
	if b == a {
		t.Error("a changed row kept the fingerprint")
	}
	write(t, filepath.Join(root, lang.Dir, "notes.txt"), "not a row")
	if c := languagesFingerprint(root); c != b {
		t.Error("a file that is not a row moved the fingerprint")
	}
	write(t, filepath.Join(root, lang.Dir, "zz.toml"), "name = \"zz\"\n")
	if d := languagesFingerprint(root); d == b {
		t.Error("a second row kept the fingerprint")
	}
}

func TestLawsFingerprint_AStampedViewIsPartOfTheKey(t *testing.T) {
	law, _ := ParseLaw(secretJavaLaw, "secret_java")
	v1, v2, cur := withView(law, 1), withView(law, 2), law
	if lawsFingerprint([]Law{v1}) == lawsFingerprint([]Law{v2}) {
		t.Error("view 1 and view 2 share a cache key")
	}
	if lawsFingerprint([]Law{v1}) == lawsFingerprint([]Law{cur}) {
		t.Error("a legacy law shares a cache key with a current one")
	}
}

func TestCheck_ARepositoryRowLexesItsFilesAndItsViewMovesBaselines(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), `name = "lua"
extensions = [".lua"]
view = 4

[comments]
line = ["--"]
block = ["--[[ ]]"]

[string.double]
open = '"'
escape = "backslash"

[string.single]
open = "'"
escape = "backslash"
`)
	writeLaw(t, root, "secret_lua", strings.NewReplacer("secret_java", "secret_lua", "*.java", "*.lua").Replace(secretJavaLaw))
	lua := "x = 1 -- it's\nSECRET = 'a'\n"
	write(t, filepath.Join(root, "app", "a.lua"), lua)

	// Stamped at 3 the row, which took effect at 4, does not read yet: the
	// apostrophe in the comment blanks the next line under the default lexer.
	write(t, filepath.Join(root, ".ratchet", "baselines", "secret_lua.txt"), "# scan-view: 3\n")
	res, err := Check(Options{Root: root})
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("view 3 read by the default lexer: findings=%+v err=%v, want none", res.Findings, err)
	}
	// Stamped at 4 the row reads, and SECRET on line 2 is a hit.
	write(t, filepath.Join(root, ".ratchet", "baselines", "secret_lua.txt"), "# scan-view: 4\n")
	res, err = Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 2 {
		t.Fatalf("view 4 read by the repository row: findings=%+v, want the SECRET on line 2", res.Findings)
	}
}

func TestLoadLaws_ABrokenRepositoryRowFailsTheLoad(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "secret_java", secretJavaLaw)
	write(t, filepath.Join(root, lang.Dir, "bad.toml"), "name = \"bad\"\nbogus = true\n")
	_, err := LoadLaws(root)
	if err == nil || !strings.Contains(err.Error(), ".ratchet/languages/bad.toml") {
		t.Fatalf("LoadLaws err = %v, want it to name the row", err)
	}
	if _, err := Check(Options{Root: root}); err == nil {
		t.Error("Check accepted a repository with a broken language row")
	}
}

func TestMigratedBaselineText_OnlyAStampBelowTheLawsViewIsAMigration(t *testing.T) {
	root := javaRepo(t, "")
	row := "app/A.java | a \"b\" \"c SECRET\n"
	cases := []struct {
		name, text string
		want       bool
	}{
		{"unstamped", row, true},
		{"stamped at 1", "# scan-view: 1\n" + row, true},
		{"stamped at 2", "# scan-view: 2\n" + row, true},
		{"stamped at the law's view", "# scan-view: 3\n" + row, false},
		{"stamped above it", "# scan-view: 4\n" + row, false},
	}
	for _, c := range cases {
		text, ok, err := MigratedBaselineText(Options{Root: root}, ".ratchet/baselines/secret_java.txt", c.text)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if ok != c.want {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.want)
		}
		if ok && text != "# scan-view: 3\n" {
			t.Errorf("%s: recomputed = %q, want the stamp alone: the Java row masks the text block", c.name, text)
		}
	}
	if _, ok, _ := MigratedBaselineText(Options{Root: root}, ".ratchet/baselines/other.txt", row); ok {
		t.Error("a baseline no law declares is no migration")
	}
}
