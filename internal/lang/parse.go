package lang

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	toml "github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Parse reads one language row. file names the source in error messages; it
// is the row's path for a repo file and the embedded name for a default.
func Parse(text, file string) (Language, error) {
	doc, err := toml.Parse(text)
	if err != nil {
		return Language{}, fmt.Errorf("%s: %w", file, err)
	}
	var l Language
	root := toml.NewFields(doc.Root, file)
	l.Name = root.Str("name")
	l.Extensions = root.List("extensions")
	l.Filenames = root.List("filenames")
	l.CodeEscape = root.Flag("code_escape")
	l.View = root.Num("view")
	l.Earlier = root.Str("earlier")
	if err := root.Finish(); err != nil {
		return Language{}, err
	}
	if !root.Has("view") {
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
	for _, t := range doc.Tables {
		if !knownSection(t.Name) {
			return Language{}, fmt.Errorf("%s: unknown table [%s] (line %d)", file, t.Name, t.Line)
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

func parseComments(doc *toml.Document, file string, l *Language) error {
	t := doc.Section("comments")
	if t == nil {
		return nil
	}
	f := toml.NewFields(t, file)
	wordStart := f.Flag("line_word_start")
	nested := f.Flag("block_nested")
	lines, blocks := f.List("line"), f.List("block")
	except := f.List("line_except")
	if err := f.Finish(); err != nil {
		return err
	}
	for _, marker := range lines {
		if marker == "" {
			return fmt.Errorf("%s: [comments] line: an empty marker", file)
		}
		l.LineComments = append(l.LineComments, LineComment{Marker: marker, WordStart: wordStart})
	}
	for _, opener := range except {
		if !continuesAMarker(opener, lines) {
			return fmt.Errorf("%s: [comments] line_except %q must begin with a line marker and continue past it", file, opener)
		}
	}
	l.LineExcept = except
	for _, pair := range blocks {
		parts := strings.Fields(pair)
		if len(parts) != 2 {
			return fmt.Errorf("%s: [comments] block: %q must be an opener and a closer separated by a space", file, pair)
		}
		l.BlockComments = append(l.BlockComments, BlockComment{Open: parts[0], Close: parts[1], Nested: nested})
	}
	return nil
}

// continuesAMarker reports whether opener begins with one of the markers and
// is longer than it.
func continuesAMarker(opener string, markers []string) bool {
	for _, marker := range markers {
		if strings.HasPrefix(opener, marker) && len(opener) > len(marker) {
			return true
		}
	}
	return false
}

func parseStrings(doc *toml.Document, file string, l *Language) error {
	for _, t := range doc.Subsections("string") {
		f := toml.NewFields(t, file)
		s := StringForm{ID: strings.TrimPrefix(t.Name, "string.")}
		s.Open = f.Str("open")
		s.Close = f.Str("close")
		esc := f.Str("escape")
		s.Multiline = f.Flag("multiline")
		s.LineStart = f.Flag("line_start")
		s.OpensAfter = f.Str("opens_after")
		s.BlankOpen = f.Flag("blank_open")
		s.CharLiteral = f.Flag("char_literal")
		s.Heredoc = f.Flag("heredoc")
		if err := f.Finish(); err != nil {
			return err
		}
		if s.Open == "" {
			return fmt.Errorf("%s: [%s] needs an `open`", file, t.Name)
		}
		if s.Heredoc {
			if err := heredocForm(&s, f.Has("close"), esc, file, t.Name); err != nil {
				return err
			}
			l.Strings = append(l.Strings, s)
			continue
		}
		if !f.Has("close") {
			s.Close = s.Open
		}
		if s.Close == "" {
			return fmt.Errorf("%s: [%s] close is empty", file, t.Name)
		}
		switch Escape(esc) {
		case "":
			s.Escape = EscapeNone
		case EscapeNone, EscapeBackslash, EscapeDoubling:
			s.Escape = Escape(esc)
		default:
			return fmt.Errorf("%s: [%s] escape %q must be none, backslash or doubling", file, t.Name, esc)
		}
		if s.CharLiteral && (len(s.Open) != 1 || s.Close != s.Open) {
			return fmt.Errorf("%s: [%s] a char literal opens and closes with the same one byte", file, t.Name)
		}
		l.Strings = append(l.Strings, s)
	}
	// Longest opener first, so a triple quote is tried before the single one
	// it begins with; declaration order breaks a tie.
	sort.SliceStable(l.Strings, func(i, j int) bool { return len(l.Strings[i].Open) > len(l.Strings[j].Open) })
	return nil
}

// heredocForm checks the keys a heredoc form cannot carry and fills in what it
// implies: it runs across lines and its body has no escape.
func heredocForm(s *StringForm, hasClose bool, esc, file, table string) error {
	switch {
	case hasClose:
		return fmt.Errorf("%s: [%s] a heredoc closes on its identifier, not on a `close`", file, table)
	case esc != "":
		return fmt.Errorf("%s: [%s] a heredoc takes no escape", file, table)
	case s.CharLiteral:
		return fmt.Errorf("%s: [%s] a heredoc is not a char literal", file, table)
	case s.LineStart || s.OpensAfter != "":
		return fmt.Errorf("%s: [%s] a heredoc opens wherever its operator stands", file, table)
	case s.BlankOpen:
		return fmt.Errorf("%s: [%s] a heredoc keeps its head", file, table)
	}
	s.Close, s.Escape, s.Multiline = "", EscapeNone, true
	return nil
}

func parseSuppress(doc *toml.Document, file string, l *Language) error {
	for _, t := range doc.Subsections("suppress") {
		f := toml.NewFields(t, file)
		d := Directive{ID: strings.TrimPrefix(t.Name, "suppress.")}
		d.Kind = f.Str("kind")
		pattern := f.Str("pattern")
		reason := f.Str("reason")
		if err := f.Finish(); err != nil {
			return err
		}
		switch d.Kind {
		case KindLint, KindType, KindCoverage:
		default:
			return fmt.Errorf("%s: [%s] kind %q must be lint, type or coverage", file, t.Name, d.Kind)
		}
		re, err := regexp.Compile(pattern)
		if err != nil || pattern == "" {
			return fmt.Errorf("%s: [%s] pattern %q is not a regular expression: %v", file, t.Name, pattern, err)
		}
		d.Pattern = re
		if reason != "" {
			if d.Reason, err = regexp.Compile(reason); err != nil {
				return fmt.Errorf("%s: [%s] reason %q is not a regular expression: %v", file, t.Name, reason, err)
			}
		}
		l.Suppress = append(l.Suppress, d)
	}
	return nil
}

func parseTests(doc *toml.Document, file string, l *Language) error {
	t := doc.Section("tests")
	if t == nil {
		return nil
	}
	f := toml.NewFields(t, file)
	patterns := f.List("patterns")
	selectable := f.List("selectable")
	declarations := f.List("declarations")
	if err := f.Finish(); err != nil {
		return err
	}
	for _, p := range declarations {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("%s: [tests] declaration %q is not a regular expression: %v", file, p, err)
		}
		l.Declarations = append(l.Declarations, re)
	}
	var err error
	if l.Tests, err = compileNamed(patterns, "pattern", file); err != nil {
		return err
	}
	l.Selectable, err = compileNamed(selectable, "selectable pattern", file)
	return err
}

// compileNamed compiles test patterns that each capture a test's name in
// exactly one group; what names the key in an error.
func compileNamed(patterns []string, what, file string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: [tests] %s %q is not a regular expression: %v", file, what, p, err)
		}
		if re.NumSubexp() != 1 {
			return nil, fmt.Errorf("%s: [tests] %s %q must capture the test's name in exactly one group, it has %d", file, what, p, re.NumSubexp())
		}
		out = append(out, re)
	}
	return out, nil
}
