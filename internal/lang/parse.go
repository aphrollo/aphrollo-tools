package lang

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Parse reads one language row. file names the source in error messages; it
// is the row's path for a repo file and the embedded name for a default.
func Parse(text, file string) (Language, error) {
	doc, err := readDocument(text)
	if err != nil {
		return Language{}, fmt.Errorf("%s: %w", file, err)
	}
	var l Language
	root := newFields(doc.root, file)
	l.Name = root.str("name")
	l.Extensions = root.list("extensions")
	l.Filenames = root.list("filenames")
	l.CodeEscape = root.flag("code_escape")
	l.View = root.num("view")
	if err := root.finish(); err != nil {
		return Language{}, err
	}
	if !root.has("view") {
		l.View = 1
	}
	if l.View < 1 {
		return Language{}, fmt.Errorf("%s: view %d must be 1 or more", file, l.View)
	}
	if !nameRe.MatchString(l.Name) {
		return Language{}, fmt.Errorf("%s: name %q must be lowercase letters, digits, `-` or `_`", file, l.Name)
	}
	for _, ext := range l.Extensions {
		if len(ext) < 2 || ext[0] != '.' || ext != strings.ToLower(ext) || strings.ContainsAny(ext, `/\`) {
			return Language{}, fmt.Errorf("%s: extension %q must be a lowercase suffix with its dot, like \".rs\"", file, ext)
		}
	}
	for _, name := range l.Filenames {
		if name == "" || strings.ContainsAny(name, `/\`) {
			return Language{}, fmt.Errorf("%s: file name %q must be a base name", file, name)
		}
	}
	if err := parseComments(doc, file, &l); err != nil {
		return Language{}, err
	}
	if err := parseStrings(doc, file, &l); err != nil {
		return Language{}, err
	}
	if err := parseSuppress(doc, file, &l); err != nil {
		return Language{}, err
	}
	if err := parseTests(doc, file, &l); err != nil {
		return Language{}, err
	}
	for _, t := range doc.tables {
		if !knownSection(t.name) {
			return Language{}, fmt.Errorf("%s: unknown table [%s] (line %d)", file, t.name, t.line)
		}
	}
	return l, nil
}

func knownSection(name string) bool {
	switch name {
	case "comments", "tests":
		return true
	}
	return strings.HasPrefix(name, "string.") || strings.HasPrefix(name, "suppress.")
}

func parseComments(doc *document, file string, l *Language) error {
	t := doc.section("comments")
	if t == nil {
		return nil
	}
	f := newFields(t, file)
	wordStart := f.flag("line_word_start")
	nested := f.flag("block_nested")
	lines, blocks := f.list("line"), f.list("block")
	if err := f.finish(); err != nil {
		return err
	}
	for _, marker := range lines {
		if marker == "" {
			return fmt.Errorf("%s: [comments] line: an empty marker", file)
		}
		l.LineComments = append(l.LineComments, LineComment{Marker: marker, WordStart: wordStart})
	}
	for _, pair := range blocks {
		parts := strings.Fields(pair)
		if len(parts) != 2 {
			return fmt.Errorf("%s: [comments] block: %q must be an opener and a closer separated by a space", file, pair)
		}
		l.BlockComments = append(l.BlockComments, BlockComment{Open: parts[0], Close: parts[1], Nested: nested})
	}
	return nil
}

func parseStrings(doc *document, file string, l *Language) error {
	for _, t := range doc.subsections("string") {
		f := newFields(t, file)
		s := StringForm{ID: strings.TrimPrefix(t.name, "string.")}
		s.Open = f.str("open")
		s.Close = f.str("close")
		esc := f.str("escape")
		s.Multiline = f.flag("multiline")
		s.LineStart = f.flag("line_start")
		s.OpensAfter = f.str("opens_after")
		s.BlankOpen = f.flag("blank_open")
		s.CharLiteral = f.flag("char_literal")
		if err := f.finish(); err != nil {
			return err
		}
		if s.Open == "" {
			return fmt.Errorf("%s: [%s] needs an `open`", file, t.name)
		}
		if !f.has("close") {
			s.Close = s.Open
		}
		if s.Close == "" {
			return fmt.Errorf("%s: [%s] close is empty", file, t.name)
		}
		switch Escape(esc) {
		case "":
			s.Escape = EscapeNone
		case EscapeNone, EscapeBackslash, EscapeDoubling:
			s.Escape = Escape(esc)
		default:
			return fmt.Errorf("%s: [%s] escape %q must be none, backslash or doubling", file, t.name, esc)
		}
		if s.CharLiteral && (len(s.Open) != 1 || s.Close != s.Open) {
			return fmt.Errorf("%s: [%s] a char literal opens and closes with the same one byte", file, t.name)
		}
		l.Strings = append(l.Strings, s)
	}
	// Longest opener first, so a triple quote is tried before the single one
	// it begins with; declaration order breaks a tie.
	sort.SliceStable(l.Strings, func(i, j int) bool { return len(l.Strings[i].Open) > len(l.Strings[j].Open) })
	return nil
}

func parseSuppress(doc *document, file string, l *Language) error {
	for _, t := range doc.subsections("suppress") {
		f := newFields(t, file)
		d := Directive{ID: strings.TrimPrefix(t.name, "suppress.")}
		d.Kind = f.str("kind")
		pattern := f.str("pattern")
		reason := f.str("reason")
		if err := f.finish(); err != nil {
			return err
		}
		switch d.Kind {
		case KindLint, KindType, KindCoverage:
		default:
			return fmt.Errorf("%s: [%s] kind %q must be lint, type or coverage", file, t.name, d.Kind)
		}
		re, err := regexp.Compile(pattern)
		if err != nil || pattern == "" {
			return fmt.Errorf("%s: [%s] pattern %q is not a regular expression: %v", file, t.name, pattern, err)
		}
		d.Pattern = re
		if reason != "" {
			if d.Reason, err = regexp.Compile(reason); err != nil {
				return fmt.Errorf("%s: [%s] reason %q is not a regular expression: %v", file, t.name, reason, err)
			}
		}
		l.Suppress = append(l.Suppress, d)
	}
	return nil
}

func parseTests(doc *document, file string, l *Language) error {
	t := doc.section("tests")
	if t == nil {
		return nil
	}
	f := newFields(t, file)
	patterns := f.list("patterns")
	if err := f.finish(); err != nil {
		return err
	}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("%s: [tests] pattern %q is not a regular expression: %v", file, p, err)
		}
		if re.NumSubexp() != 1 {
			return fmt.Errorf("%s: [tests] pattern %q must capture the test's name in exactly one group, it has %d", file, p, re.NumSubexp())
		}
		l.Tests = append(l.Tests, re)
	}
	return nil
}
