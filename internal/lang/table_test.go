package lang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustRow(t *testing.T, text string) Language {
	t.Helper()
	l, err := Parse(text, "row.toml")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestDefaults_EveryEmbeddedRowParsesAndNamesItsFile(t *testing.T) {
	tbl, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"csharp", "default", "default-hash", "go", "java", "javascript", "kotlin", "php", "python", "ruby", "rust", "shell", "toml", "typescript", "yaml"}
	var got []string
	for _, r := range tbl.Rows() {
		got = append(got, r.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func TestDefaults_ViewVersionsMatchWhenEachLexerTookEffect(t *testing.T) {
	tbl, _ := Defaults()
	want := map[string]int{
		"rust": 1, "python": 2, "shell": 2, "toml": 2, "ruby": 2, "yaml": 2,
		"java": 3, "csharp": 3, "kotlin": 3, "php": 3,
	}
	for name, view := range want {
		row, ok := tbl.Named(name)
		if !ok || row.View != view {
			t.Errorf("%s view = %d (found %v), want %d", name, row.View, ok, view)
		}
	}
}

func TestFor_FindsByExtensionCaseInsensitivelyAndByFileName(t *testing.T) {
	base, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := base.Extend([]Language{mustRow(t, "name = \"make\"\nextensions = [\".mk\"]\nfilenames = [\"Makefile\"]\n")})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"src/lib.rs":          "rust",
		"SRC/LIB.RS":          "rust",
		"a.b.py":              "python",
		"ci.YML":              "yaml",
		`dir\win.go`:          "go",
		"Makefile":            "make",
		"sub/dir/Makefile":    "make",
		"rules.mk":            "make",
		"Main.java":           "java",
		"x.kts":               "kotlin",
		"Program.cs":          "csharp",
		"index.php":           "php",
		"component.test.tsx":  "typescript",
		"server.mjs":          "javascript",
		"script.sh":           "shell",
		"Cargo.toml":          "toml",
		"gem.rb":              "ruby",
		"makefile":            "",
		"README":              "",
		"notes.txt":           "",
		"archive.tar.gz":      "",
		"dir.rs/inner":        "",
		".rs":                 "rust",
		"dir/.hidden/file.rs": "rust",
	}
	for file, want := range cases {
		row, ok := tbl.For(file)
		if want == "" {
			if ok {
				t.Errorf("%s -> %s, want no row", file, row.Name)
			}
			continue
		}
		if !ok || row.Name != want {
			t.Errorf("%s -> %q (found %v), want %s", file, row.Name, ok, want)
		}
	}
}

func TestNamed_FindsNeutralRows(t *testing.T) {
	tbl, _ := Defaults()
	for _, name := range []string{Neutral, NeutralHash} {
		row, ok := tbl.Named(name)
		if !ok || len(row.Extensions) != 0 {
			t.Errorf("%s: found %v with extensions %v, want a row that owns no extension", name, ok, row.Extensions)
		}
	}
	if _, ok := tbl.Named("nosuch"); ok {
		t.Error("found a row that does not exist")
	}
}

func TestExtend_ReplacesByNameAndTakesExtensions(t *testing.T) {
	base, _ := Defaults()
	mine := mustRow(t, "name = \"rust\"\nextensions = [\".rs\", \".rsx\"]\nview = 7\n")
	tbl, err := base.Extend([]Language{mine})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := tbl.For("a.rsx")
	if row.Name != "rust" || row.View != 7 {
		t.Errorf("the repo's rust row must replace the default: %+v", row)
	}
	if def, _ := base.For("a.rs"); def.View != 1 {
		t.Errorf("Extend changed the embedded table: %+v", def)
	}
}

func TestExtend_ARowTakesAnExtensionFromTheRowThatHeldIt(t *testing.T) {
	base, _ := Defaults()
	mine := mustRow(t, "name = \"mine\"\nextensions = [\".py\"]\nfilenames = [\"Rakefile\"]\n")
	tbl, err := base.Extend([]Language{mine})
	if err != nil {
		t.Fatal(err)
	}
	if row, _ := tbl.For("a.py"); row.Name != "mine" {
		t.Errorf(".py -> %s, want mine", row.Name)
	}
	if row, _ := tbl.Named("python"); len(row.Extensions) != 0 {
		t.Errorf("python still claims %v", row.Extensions)
	}
	if row, _ := tbl.For("Rakefile"); row.Name != "mine" {
		t.Errorf("Rakefile -> %s", row.Name)
	}
}

func TestExtend_RefusesTwoNewRowsClaimingOneExtension(t *testing.T) {
	base, _ := Defaults()
	a := mustRow(t, "name = \"a\"\nextensions = [\".zz\"]\n")
	b := mustRow(t, "name = \"b\"\nextensions = [\".zz\"]\n")
	if _, err := base.Extend([]Language{a, b}); err == nil || !strings.Contains(err.Error(), ".zz is claimed by both") {
		t.Errorf("err = %v", err)
	}
	c := mustRow(t, "name = \"c\"\nfilenames = [\"F\"]\n")
	d := mustRow(t, "name = \"d\"\nfilenames = [\"F\"]\n")
	if _, err := base.Extend([]Language{c, d}); err == nil || !strings.Contains(err.Error(), "file name F is claimed by both") {
		t.Errorf("err = %v", err)
	}
	if _, err := build([]Language{a, a}); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Errorf("err = %v", err)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestForRoot_ReadsTheRepoRowsOverTheDefaults(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "lua.toml"), "name = \"lua\"\nextensions = [\".lua\"]\n[comments]\nline = [\"--\"]\n")
	writeFile(t, filepath.Join(root, Dir, "notes.txt"), "not a row")
	tbl, err := ForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := tbl.For("x.lua")
	if !ok || row.LineComments[0].Marker != "--" {
		t.Errorf("lua row = %+v found %v", row, ok)
	}
	if _, ok := tbl.For("x.rs"); !ok {
		t.Error("the defaults must survive a repo row")
	}
}

func TestForRoot_NoDirectoryIsTheDefaults(t *testing.T) {
	base, _ := Defaults()
	for _, root := range []string{"", t.TempDir()} {
		tbl, err := ForRoot(root)
		if err != nil || tbl != base {
			t.Errorf("root %q: table %p, want the shared defaults %p (err %v)", root, tbl, base, err)
		}
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if tbl, err := ForRoot(root); err != nil || tbl != base {
		t.Errorf("an empty languages directory is the defaults: %v", err)
	}
}

func TestForRoot_RefusesARowWhoseNameIsNotItsFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "lua.toml"), "name = \"luau\"\n")
	if _, err := ForRoot(root); err == nil || !strings.Contains(err.Error(), `name "luau" must equal the file's stem "lua"`) {
		t.Errorf("err = %v", err)
	}
}

func TestForRoot_ReportsABrokenRow(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "bad.toml"), "name = \"bad\"\nbogus = true\n")
	_, err := ForRoot(root)
	if err == nil || !strings.Contains(err.Error(), ".ratchet/languages/bad.toml") || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("err = %v", err)
	}
}
