package ratchet

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// A law that states no comment_prefix reads what opens a comment from the
// language table's row for its scope: one row, or rows that agree, name it;
// rows that disagree, a scope that names no row, or none at all keep the
// neutral row's `//`.
func TestCommentPrefix_TheDefaultIsTheTablesLineCommentOfTheScope(t *testing.T) {
	cases := []struct {
		name  string
		globs []string
		want  string
	}{
		{"go is lexed as the neutral row", []string{"**/*.go"}, "//"},
		{"rust", []string{"**/*.rs"}, "//"},
		{"typescript", []string{"**/*.ts", "**/*.tsx"}, "//"},
		{"python", []string{"**/*.py"}, "#"},
		{"shell", []string{"scripts/*.sh"}, "#"},
		{"toml by extension", []string{"**/*.toml"}, "#"},
		{"yaml, two extensions", []string{"**/*.yaml", "**/*.yml"}, "#"},
		{"ruby", []string{"**/*.rb"}, "#"},
		{"php reads its first marker", []string{"**/*.php"}, "//"},
		{"rows that agree", []string{"**/*.py", "**/*.sh"}, "#"},
		{"rows that disagree", []string{"**/*.py", "**/*.rs"}, "//"},
		{"a glob naming no row votes for the neutral row", []string{"**/*.py", "**/*.md"}, "//"},
		{"prose alone", []string{"**/*.md"}, "//"},
		{"a file name the row owns", []string{"**/Cargo.toml"}, "#"},
		{"everything", []string{"**/*"}, "//"},
	}
	for _, c := range cases {
		if got := lawOver(t, c.globs...).commentPrefix(); got != c.want {
			t.Errorf("%s: commentPrefix = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCommentPrefix_AnExplicitPrefixWinsOverTheTable(t *testing.T) {
	l := lawOver(t, "**/*.py")
	l.CommentPrefix = ";;"
	if got := l.commentPrefix(); got != ";;" {
		t.Errorf("commentPrefix = %q, want the law's own", got)
	}
}

func TestCommentPrefix_ALawLiteralWithNoResolvedScopeKeepsTheNeutralMarker(t *testing.T) {
	if got := (Law{}).commentPrefix(); got != "//" {
		t.Errorf("commentPrefix = %q, want //", got)
	}
}

// A repository's own row is read as the engine reads it everywhere else: its
// comment marker is the default of a law over its extension.
func TestCommentPrefix_ARepositoryRowNamesTheMarkerOfItsLanguage(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".ratchet", "languages", "lua.toml"), "name = \"lua\"\nextensions = [\".lua\"]\n\n[comments]\nline = [\"--\"]\n")
	writeLaw(t, root, "lua_law", "name = \"lua_law\"\ndescription = \"x\"\nseverity = \"deny\"\n[scope]\ninclude = [\"**/*.lua\"]\n[matcher]\nkind = \"symbol-removed\"\npattern = \"(a)\"\n")
	laws, err := LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(laws) != 1 || laws[0].commentPrefix() != "--" {
		t.Fatalf("laws = %d, commentPrefix = %q, want one law reading --", len(laws), laws[0].commentPrefix())
	}
}

// The neutral row is the table's: a repository that redefines it changes what
// opens a comment for every scope that names no row, and a table with no
// neutral row, or one with no line comment, falls back to `//`.
func TestCommentPrefix_TheNeutralMarkerIsTheNeutralRowsFirstLineComment(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".ratchet", "languages", "default.toml"), "name = \"default\"\n\n[comments]\nline = [\"%\", \"//\"]\n")
	writeLaw(t, root, "prose_law", "name = \"prose_law\"\ndescription = \"x\"\nseverity = \"deny\"\n[scope]\ninclude = [\"**/*.md\"]\n[matcher]\nkind = \"symbol-removed\"\npattern = \"(a)\"\n")
	laws, err := LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := laws[0].commentPrefix(); got != "%" {
		t.Errorf("commentPrefix = %q, want the redefined neutral row's %%", got)
	}
	empty, err := (&lang.Table{}).Extend(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := neutralMarker(empty); got != "//" {
		t.Errorf("a table with no neutral row: %q, want //", got)
	}
	bare, err := (&lang.Table{}).Extend([]lang.Language{{Name: lang.Neutral}})
	if err != nil {
		t.Fatal(err)
	}
	if got := neutralMarker(bare); got != "//" {
		t.Errorf("a neutral row with no line comment: %q, want //", got)
	}
}

// Parity: every law this repository declares and every embedded preset whose
// output depends on its comment prefix — it strips comments, walks a comment
// run, or owns a marker kind — keeps the prefix it had when the default was a
// fixed `//`. A law over a TOML file that only greps it never reads its prefix
// and so has none to keep.
func TestCommentPrefix_EveryDeclaredLawAndPresetKeepsItsPrefix(t *testing.T) {
	_, file, _, _ := runtime.Caller(0) // tree-read-ok: the laws are this repository's own
	laws, err := LoadLaws(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	if err != nil {
		t.Fatal(err)
	}
	checked := len(laws)
	for _, l := range laws {
		assertLegacyPrefix(t, "law "+l.Name, l)
	}
	entries, err := ListPresets()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := LoadPresetText(e.Group, e.Name)
		if err != nil {
			t.Fatal(err)
		}
		rendered, missing := RenderPresetText(raw, nil)
		if len(missing) > 0 {
			continue // its scope is a parameter a repository states
		}
		l, err := ParseLaw(rendered, e.Name)
		if err != nil {
			t.Fatalf("preset %s/%s: %v", e.Group, e.Name, err)
		}
		assertLegacyPrefix(t, "preset "+e.Group+"/"+e.Name, l)
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d laws and presets checked; the walk is broken", checked)
	}
}

func assertLegacyPrefix(t *testing.T, what string, l Law) {
	t.Helper()
	readsPrefix := l.CodeOnly || l.Contiguous || l.Matcher.Kind == KindMarkerWithinLines || l.Matcher.Kind == KindRegexNear
	if !readsPrefix {
		return
	}
	want := "//"
	if l.CommentPrefix != "" {
		want = l.CommentPrefix
	}
	if got := l.commentPrefix(); got != want {
		t.Errorf("%s: commentPrefix = %q, want %q (scope %s)", what, got, want, strings.Join(l.Scope.Include, ", "))
	}
}
