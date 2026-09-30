package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
	"github.com/aphrollo/aphrollo-tools/internal/mask"
)

// A language row nobody proved lexes nothing right: a quote it reads as
// opening a string that never closes blanks every line below it, and every law
// over those files then passes over code it never saw. So a row is proved the
// way a law is, against files. `.ratchet/fixtures/languages/<row>/` holds
// source files the row owns (by extension or file name) and, beside each, the
// answer a row must give:
//
//	<file>.masked      the file with its strings and comments blanked to
//	                   spaces (newlines kept), byte for byte
//	<file>.tests       the test names the row's test patterns capture, one per
//	                   line, in order of appearance within each pattern
//	<file>.suppressed  the kinds (lint, type, coverage) of suppression the row's
//	                   directives find, one per line, in that order
//
// An empty answer file means the row must find nothing. A row that declares
// comments, strings or an escape needs a `.masked` fixture, one with test
// patterns a `.tests` one, one with directives a `.suppressed` one.
const LanguageFixturesDir = FixturesDir + "/languages"

// languageLawPrefix names a language row's result among the law results.
const languageLawPrefix = "language/"

var languageAnswerSuffixes = []string{".masked", ".tests", ".suppressed"}

// RunLanguageFixtures proves every row of root's language table that has
// fixtures, and refuses a row of the repository's own (`.ratchet/languages`)
// that has none. A fixture directory naming no row is refused too: it proves
// nothing. The embedded defaults are proved only where a repository ships
// their fixtures, as this one does.
func RunLanguageFixtures(root string) ([]FixtureResult, error) {
	tbl, err := lang.ForRoot(root)
	if err != nil {
		return nil, err
	}
	own, err := repositoryRows(root)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(root, filepath.FromSlash(LanguageFixturesDir))
	var out []FixtureResult
	proved := map[string]bool{}
	for _, row := range tbl.Rows() {
		res := FixtureResult{Law: languageLawPrefix + row.Name}
		rel := LanguageFixturesDir + "/" + row.Name
		if !isDir(filepath.Join(base, row.Name)) {
			if own[row.Name] {
				res.Failures = append(res.Failures, fmt.Sprintf("no fixtures at %s — a language nobody proved lexes nothing right", rel))
				out = append(out, res)
			}
			continue
		}
		proved[row.Name] = true
		out = append(out, proveLanguage(tbl, row, filepath.Join(base, row.Name), rel, own[row.Name]))
	}
	entries, err := os.ReadDir(base)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", LanguageFixturesDir, err)
	}
	for _, e := range entries {
		if e.IsDir() && !proved[e.Name()] {
			out = append(out, FixtureResult{Law: languageLawPrefix + e.Name(), Failures: []string{fmt.Sprintf(
				"%s/%s names no language row — add %s/%s.toml or remove the fixtures", LanguageFixturesDir, e.Name(), lang.Dir, e.Name())}})
		}
	}
	slices.SortStableFunc(out, func(a, b FixtureResult) int { return strings.Compare(a.Law, b.Law) })
	return out, nil
}

// HasLanguages reports whether the repository defines language rows of its own.
func HasLanguages(root string) bool {
	own, err := repositoryRows(root)
	return err == nil && len(own) > 0
}

// repositoryRows is the set of row names the repository itself defines.
func repositoryRows(root string) (map[string]bool, error) {
	dir := filepath.Join(root, filepath.FromSlash(lang.Dir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", lang.Dir, err)
	}
	own := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			own[strings.TrimSuffix(e.Name(), ".toml")] = true
		}
	}
	return own, nil
}

func answerSuffix(name string) string {
	for _, s := range languageAnswerSuffixes {
		if strings.HasSuffix(name, s) {
			return s
		}
	}
	return ""
}

// proveLanguage judges one row against the files in dir. HitFiles counts the
// source fixtures it was proved on.
func proveLanguage(tbl *lang.Table, row lang.Language, dir, rel string, own bool) FixtureResult {
	res := FixtureResult{Law: languageLawPrefix + row.Name}
	fail := func(format string, args ...any) { res.Failures = append(res.Failures, fmt.Sprintf(format, args...)) }
	entries, err := os.ReadDir(dir)
	if err != nil {
		fail("reading %s: %v", rel, err)
		return res
	}
	var sources []string
	answers := map[string]bool{}
	for _, e := range entries {
		switch {
		case e.IsDir():
			fail("%s/%s is a directory — fixtures are files", rel, e.Name())
		case answerSuffix(e.Name()) != "":
			answers[e.Name()] = true
		default:
			sources = append(sources, e.Name())
		}
	}
	for name := range answers {
		source := strings.TrimSuffix(name, answerSuffix(name))
		if !isFile(filepath.Join(dir, source)) {
			fail("%s/%s answers a file that is not there (%s)", rel, name, source)
		}
	}
	if own && len(row.Extensions) == 0 && len(row.Filenames) == 0 {
		fail("the row owns no extension and no file name, so no file is read by it")
	}
	res.HitFiles = len(sources)
	if len(sources) == 0 {
		fail("no source fixture at %s — a language nobody proved lexes nothing right", rel)
	}
	proved := map[string]bool{}
	for _, name := range sources {
		path := filepath.Join(dir, name)
		if owner, ok := tbl.For(name); !ok || owner.Name != row.Name {
			fail("%s/%s is read by %s, not by this row — name it with an extension or file name the row owns", rel, name, rowNameOf(owner, ok))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fail("reading %s/%s: %v", rel, name, err)
			continue
		}
		src := string(data)
		lexer := mask.ForFile(tbl, name, 0)
		answered := false
		for _, suffix := range languageAnswerSuffixes {
			if !answers[name+suffix] {
				continue
			}
			answered = true
			want, err := os.ReadFile(path + suffix)
			if err != nil {
				fail("reading %s/%s%s: %v", rel, name, suffix, err)
				continue
			}
			proved[suffix] = true
			if msg := checkLanguageAnswer(row, suffix, src, lexer, string(want)); msg != "" {
				fail("%s/%s%s: %s", rel, name, suffix, msg)
			}
		}
		if !answered {
			fail("%s/%s has no answer beside it (%s)", rel, name, strings.Join(languageAnswerSuffixes, ", "))
		}
	}
	if row.Lexes() && !proved[".masked"] {
		fail("the row declares comments or strings and no fixture has a .masked answer")
	}
	if len(row.Tests) > 0 && !proved[".tests"] {
		fail("the row declares test patterns and no fixture has a .tests answer")
	}
	if len(row.Suppress) > 0 && !proved[".suppressed"] {
		fail("the row declares directives and no fixture has a .suppressed answer")
	}
	return res
}

func rowNameOf(row lang.Language, ok bool) string {
	if !ok {
		return "no row"
	}
	return "row " + row.Name
}

// checkLanguageAnswer compares what the row gives for src with the answer file
// and names the first difference, or returns "" when they agree.
func checkLanguageAnswer(row lang.Language, suffix, src string, lexer *mask.Lexer, want string) string {
	switch suffix {
	case ".masked":
		return firstLineDifference(lexer.Lex(src, true, true), want)
	case ".tests":
		return listDifference(row.TestNames(src), want)
	default:
		return listDifference(suppressionKinds(row, lexer.Lex(src, true, false)), want)
	}
}

// suppressionKinds are the kinds of suppression the row's directives find in
// directives, the comment-preserving view, in a fixed order.
func suppressionKinds(row lang.Language, directives string) []string {
	var kinds []string
	for _, kind := range []string{lang.KindLint, lang.KindType, lang.KindCoverage} {
		if lang.Suppressed([]lang.Language{row}, kind, directives) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// listDifference compares a list to an answer file's lines.
func listDifference(got []string, want string) string {
	var wantLines []string
	if trimmed := strings.TrimRight(want, "\n"); trimmed != "" {
		wantLines = strings.Split(trimmed, "\n")
	}
	if strings.Join(got, "\n") == strings.Join(wantLines, "\n") && len(got) == len(wantLines) {
		return ""
	}
	return fmt.Sprintf("the row gives %q, the fixture expects %q", got, wantLines)
}

// firstLineDifference names the first line at which got and want differ.
func firstLineDifference(got, want string) string {
	if got == want {
		return ""
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		g, w := lineAt(gotLines, i), lineAt(wantLines, i)
		if g != w {
			return fmt.Sprintf("line %d: the row gives %q, the fixture expects %q", i+1, g, w)
		}
	}
	return "the row's output differs from the fixture"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of file>"
}
