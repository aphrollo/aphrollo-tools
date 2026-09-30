package ratchet

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

const luaRow = `name = "lua"
extensions = [".lua"]

[comments]
line = ["--"]
block = ["--[[ ]]"]

[string.double]
open = '"'
escape = "backslash"

[suppress.luacheck]
kind = "lint"
pattern = 'luacheck:\s*ignore'

[tests]
patterns = ['(?m)^describe\("([^"]+)"']
`

// luaMasked is a.lua with its strings and comments blanked: the string "adds"
// is 4 bytes, the comment `-- it's a comment` 17 after a space, `"s"` 1, and
// `-- luacheck: ignore` 19 after a space.
var luaMasked = "describe(\"" + strings.Repeat(" ", 4) + "\", f)" + strings.Repeat(" ", 1+17) + "\n" +
	"x = \" \"" + strings.Repeat(" ", 1+19) + "\n"

// luaRepo is a repository with the lua row and complete fixtures for it.
func luaRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), luaRow)
	dir := filepath.Join(root, LanguageFixturesDir, "lua")
	write(t, filepath.Join(dir, "a.lua"), "describe(\"adds\", f) -- it's a comment\nx = \"s\" -- luacheck: ignore\n")
	write(t, filepath.Join(dir, "a.lua.masked"), luaMasked)
	write(t, filepath.Join(dir, "a.lua.tests"), "adds\n")
	write(t, filepath.Join(dir, "a.lua.suppressed"), "lint\n")
	return root
}

func languageResult(t *testing.T, root, name string) FixtureResult {
	t.Helper()
	results, err := RunLanguageFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Law == "language/"+name {
			return r
		}
	}
	t.Fatalf("no result for language/%s in %+v", name, results)
	return FixtureResult{}
}

func wantFailure(t *testing.T, r FixtureResult, sub string) {
	t.Helper()
	for _, f := range r.Failures {
		if strings.Contains(f, sub) {
			return
		}
	}
	t.Errorf("failures %q do not name %q", r.Failures, sub)
}

func TestRunLanguageFixtures_ARowWithCompleteFixturesIsProved(t *testing.T) {
	r := languageResult(t, luaRepo(t), "lua")
	if len(r.Failures) != 0 || r.HitFiles != 1 {
		t.Fatalf("result = %+v, want a proof on the one source fixture", r)
	}
}

func TestRunLanguageFixtures_ARepositoryRowWithNoFixturesIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), luaRow)
	wantFailure(t, languageResult(t, root, "lua"), "no fixtures at .ratchet/fixtures/languages/lua")
}

func TestRunLanguageFixtures_AnEmbeddedRowWithNoFixturesIsLeftAlone(t *testing.T) {
	results, err := RunLanguageFixtures(t.TempDir())
	if err != nil || len(results) != 0 {
		t.Fatalf("results = %+v err = %v, want none: only fixtures or a repository row make a row proved", results, err)
	}
}

func TestRunLanguageFixtures_AWrongAnswerNamesTheFirstDifferingLine(t *testing.T) {
	root := luaRepo(t)
	wrong := strings.Replace(luaMasked, "x = \" \"", "x = \"s\"", 1)
	write(t, filepath.Join(root, LanguageFixturesDir, "lua", "a.lua.masked"), wrong)
	r := languageResult(t, root, "lua")
	row := strings.Split(luaMasked, "\n")[1]
	wantFailure(t, r, "a.lua.masked: line 2: the row gives "+strconv.Quote(row)+", the fixture expects "+strconv.Quote(strings.Replace(row, "\" \"", "\"s\"", 1)))
	if len(r.Failures) != 1 {
		t.Errorf("failures = %q, want the one", r.Failures)
	}
}

func TestRunLanguageFixtures_ALineMissingFromTheAnswerIsNamed(t *testing.T) {
	root := luaRepo(t)
	first := strings.Split(luaMasked, "\n")[0]
	write(t, filepath.Join(root, LanguageFixturesDir, "lua", "a.lua.masked"), first)
	second := strings.Split(luaMasked, "\n")[1]
	wantFailure(t, languageResult(t, root, "lua"), "line 2: the row gives "+strconv.Quote(second)+`, the fixture expects "<end of file>"`)
}

func TestRunLanguageFixtures_TestNamesAndSuppressionKindsAreCompared(t *testing.T) {
	root := luaRepo(t)
	dir := filepath.Join(root, LanguageFixturesDir, "lua")
	write(t, filepath.Join(dir, "a.lua.tests"), "adds\nsubtracts\n")
	write(t, filepath.Join(dir, "a.lua.suppressed"), "")
	r := languageResult(t, root, "lua")
	wantFailure(t, r, `a.lua.tests: the row gives ["adds"], the fixture expects ["adds" "subtracts"]`)
	wantFailure(t, r, `a.lua.suppressed: the row gives ["lint"], the fixture expects []`)
}

func TestRunLanguageFixtures_AnEmptyAnswerMeansTheRowFindsNothing(t *testing.T) {
	root := luaRepo(t)
	dir := filepath.Join(root, LanguageFixturesDir, "lua")
	write(t, filepath.Join(dir, "b.lua"), "x = 1\n")
	write(t, filepath.Join(dir, "b.lua.tests"), "")
	write(t, filepath.Join(dir, "b.lua.suppressed"), "")
	write(t, filepath.Join(dir, "b.lua.masked"), "x = 1\n")
	r := languageResult(t, root, "lua")
	if len(r.Failures) != 0 || r.HitFiles != 2 {
		t.Fatalf("result = %+v, want both fixtures proved", r)
	}
}

func TestRunLanguageFixtures_EveryFeatureTheRowDeclaresNeedsItsOwnAnswer(t *testing.T) {
	for suffix, want := range map[string]string{
		".masked":     "no fixture has a .masked answer",
		".tests":      "no fixture has a .tests answer",
		".suppressed": "no fixture has a .suppressed answer",
	} {
		root := luaRepo(t)
		dir := filepath.Join(root, LanguageFixturesDir, "lua")
		if suffix != ".masked" {
			write(t, filepath.Join(dir, "a.lua.masked"), luaMasked)
		}
		if err := os.Remove(filepath.Join(dir, "a.lua"+suffix)); err != nil {
			t.Fatal(err)
		}
		wantFailure(t, languageResult(t, root, "lua"), want)
	}
}

func TestRunLanguageFixtures_ASourceWithNoAnswerAndAnAnswerWithNoSourceAreRefused(t *testing.T) {
	root := luaRepo(t)
	dir := filepath.Join(root, LanguageFixturesDir, "lua")
	write(t, filepath.Join(dir, "c.lua"), "x = 1\n")
	write(t, filepath.Join(dir, "gone.lua.masked"), "")
	r := languageResult(t, root, "lua")
	wantFailure(t, r, "c.lua has no answer beside it")
	wantFailure(t, r, "gone.lua.masked answers a file that is not there (gone.lua)")
}

func TestRunLanguageFixtures_ASourceAnotherRowOwnsIsRefused(t *testing.T) {
	root := luaRepo(t)
	dir := filepath.Join(root, LanguageFixturesDir, "lua")
	write(t, filepath.Join(dir, "x.py"), "x = 1\n")
	write(t, filepath.Join(dir, "x.py.masked"), "x = 1\n")
	write(t, filepath.Join(dir, "y.unknown"), "x = 1\n")
	write(t, filepath.Join(dir, "y.unknown.masked"), "x = 1\n")
	r := languageResult(t, root, "lua")
	wantFailure(t, r, "x.py is read by row python, not by this row")
	wantFailure(t, r, "y.unknown is read by no row, not by this row")
}

func TestRunLanguageFixtures_NoSourceAtAllAndASubdirectoryAreRefused(t *testing.T) {
	root := luaRepo(t)
	dir := filepath.Join(root, LanguageFixturesDir, "lua")
	for _, f := range []string{"a.lua", "a.lua.masked", "a.lua.tests", "a.lua.suppressed"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "nested", "a.lua"), "x\n")
	r := languageResult(t, root, "lua")
	wantFailure(t, r, "no source fixture at .ratchet/fixtures/languages/lua")
	wantFailure(t, r, "nested is a directory")
}

func TestRunLanguageFixtures_AFixtureDirectoryNamingNoRowIsRefused(t *testing.T) {
	root := luaRepo(t)
	write(t, filepath.Join(root, LanguageFixturesDir, "cobol", "a.cob"), "x\n")
	r := languageResult(t, root, "cobol")
	wantFailure(t, r, "names no language row")
}

func TestRunLanguageFixtures_ARepositoryRowThatOwnsNothingIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "orphan.toml"), "name = \"orphan\"\n[comments]\nline = [\"#\"]\n")
	write(t, filepath.Join(root, LanguageFixturesDir, "orphan", "a.txt"), "x\n")
	wantFailure(t, languageResult(t, root, "orphan"), "owns no extension and no file name")
}

func TestRunLanguageFixtures_AFileNameRowIsProvedByItsFixtureName(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "make.toml"), "name = \"make\"\nfilenames = [\"Makefile\"]\n[comments]\nline = [\"#\"]\n")
	dir := filepath.Join(root, LanguageFixturesDir, "make")
	write(t, filepath.Join(dir, "Makefile"), "all: # it's\n")
	write(t, filepath.Join(dir, "Makefile.masked"), "all:       \n")
	if r := languageResult(t, root, "make"); len(r.Failures) != 0 {
		t.Fatalf("failures = %q", r.Failures)
	}
}

func TestRunLanguageFixtures_ABrokenRepositoryRowIsAnError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, lang.Dir, "bad.toml"), "name = \"bad\"\nbogus = 1\n")
	if _, err := RunLanguageFixtures(root); err == nil {
		t.Fatal("a repository whose row does not load cannot be proved")
	}
}

func TestRunFixturesWith_ProvesLanguagesUnlessNarrowedToNamedLaws(t *testing.T) {
	root := luaRepo(t)
	write(t, filepath.Join(root, LanguageFixturesDir, "lua", "a.lua.tests"), "wrong\n")
	writeLaw(t, root, "any_law", "name = \"any_law\"\ndescription = \"x\"\nseverity = \"deny\"\n[scope]\ninclude = [\"**/*.txt\"]\n[matcher]\nkind = \"regex-absent\"\npattern = \"zzz\"\n")
	all, err := RunFixturesWith(root, FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, r := range all {
		if r.Law == "language/lua" {
			seen = true
			if len(r.Failures) == 0 {
				t.Error("the wrong .tests answer must fail the language proof")
			}
		}
	}
	if !seen {
		t.Errorf("an unnarrowed run did not prove the language: %+v", all)
	}
	only, err := RunFixturesWith(root, FixtureOptions{Only: []string{"any_law"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range only {
		if strings.HasPrefix(r.Law, "language/") {
			t.Errorf("a run narrowed to named laws proved %s", r.Law)
		}
	}
}

func TestHasLanguages_OnlyARepositoryRowCounts(t *testing.T) {
	root := t.TempDir()
	if HasLanguages(root) {
		t.Error("a repository with no rows reports languages")
	}
	write(t, filepath.Join(root, lang.Dir, "notes.txt"), "x")
	if HasLanguages(root) {
		t.Error("a file that is not a row counts")
	}
	write(t, filepath.Join(root, lang.Dir, "lua.toml"), luaRow)
	if !HasLanguages(root) {
		t.Error("a row does not count")
	}
}

// Every embedded row that owns an extension ships fixtures in this repository,
// so `aphrollo ratchet test` here proves the whole default table.
func TestRunLanguageFixtures_EveryEmbeddedRowIsProvedByThisRepositorysFixtures(t *testing.T) {
	_, file, _, _ := runtime.Caller(0) // tree-read-ok: the fixtures are this repository's own
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	results, err := RunLanguageFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	proved := map[string]FixtureResult{}
	for _, r := range results {
		if len(r.Failures) != 0 {
			t.Errorf("%s: %q", r.Law, r.Failures)
		}
		proved[strings.TrimPrefix(r.Law, "language/")] = r
	}
	tbl, _ := lang.Defaults()
	for _, row := range tbl.Rows() {
		if len(row.Extensions) == 0 {
			continue
		}
		if proved[row.Name].HitFiles == 0 {
			t.Errorf("embedded row %s has no fixtures under %s", row.Name, LanguageFixturesDir)
		}
	}
}
