package ratchet

import (
	"fmt"
	"strconv"
	"strings"
)

// The law files are TOML, and this binary carries no dependencies, so it parses
// the SUBSET a law can express and rejects everything else: root keys plus
// one level of `[table]`, values of string / integer / boolean / array-of-string.
// Rejecting rather than ignoring is the point — a law is a rule other people
// rely on, so a typo'd key must fail loudly at load, not silently disable half
// the rule.

type tomlKind int

const (
	tomlString tomlKind = iota
	tomlInt
	tomlBool
	tomlArray
)

func (k tomlKind) String() string {
	switch k {
	case tomlInt:
		return "integer"
	case tomlBool:
		return "boolean"
	case tomlArray:
		return "array of strings"
	default:
		return "string"
	}
}

// tomlValue is one parsed value; only the field matching kind is meaningful.
type tomlValue struct {
	kind tomlKind
	s    string
	i    int
	b    bool
	list []string
	line int
}

// tomlDoc is a parsed law file: section name ("" for the root) -> key -> value,
// plus the order keys were seen in, so error messages can name them stably.
type tomlDoc struct {
	sections map[string]map[string]tomlValue
	order    map[string][]string
}

func (d *tomlDoc) value(section, key string) (tomlValue, bool) {
	v, ok := d.sections[section][key]
	return v, ok
}

func (d *tomlDoc) str(section, key string) string {
	v, ok := d.value(section, key)
	if !ok || v.kind != tomlString {
		return ""
	}
	return v.s
}

func (d *tomlDoc) has(section string) bool {
	_, ok := d.sections[section]
	return ok
}

// keys lists a section's keys in the order the file declared them.
func (d *tomlDoc) keys(section string) []string { return d.order[section] }

// sectionNames lists every table name declared, root ("") excluded.
func (d *tomlDoc) sectionNames() []string {
	var out []string
	for name := range d.sections {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func parseTOML(text string) (*tomlDoc, error) {
	doc := &tomlDoc{
		sections: map[string]map[string]tomlValue{"": {}},
		order:    map[string][]string{},
	}
	section := ""
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name, err := parseTableHeader(line, lineNo)
			if err != nil {
				return nil, err
			}
			if _, seen := doc.sections[name]; seen && name != "" {
				return nil, fmt.Errorf("line %d: table [%s] is declared twice", lineNo, name)
			}
			doc.sections[name] = map[string]tomlValue{}
			section = name
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected `key = value`, got %q", lineNo, line)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\"'[]") {
			return nil, fmt.Errorf("line %d: %q is not a bare key", lineNo, key)
		}
		if _, dup := doc.sections[section][key]; dup {
			return nil, fmt.Errorf("line %d: key %q is set twice in the same table", lineNo, key)
		}
		val, err := parseValue(strings.TrimSpace(stripComment(rest)), lineNo)
		if err != nil {
			return nil, err
		}
		doc.sections[section][key] = val
		doc.order[section] = append(doc.order[section], key)
	}
	return doc, nil
}

// parseTableHeader reads `[name]`. Only ONE level is legal: a law's shape is
// flat by design, and `[matcher.extra]` would be a rule half of the engine
// never reads.
func parseTableHeader(line string, lineNo int) (string, error) {
	if !strings.HasSuffix(line, "]") {
		return "", fmt.Errorf("line %d: unterminated table header %q", lineNo, line)
	}
	name := strings.TrimSpace(line[1 : len(line)-1])
	if name == "" || strings.ContainsAny(name, " \t\"'[]") {
		return "", fmt.Errorf("line %d: %q is not a bare table name", lineNo, line)
	}
	if strings.Contains(name, ".") {
		return "", fmt.Errorf("line %d: nested table [%s] — a law file is one level deep", lineNo, name)
	}
	return name, nil
}

// stripComment removes a trailing `#` comment that starts outside a string.
func stripComment(s string) string {
	inBasic, inLiteral := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if inBasic {
				i++
			}
		case '"':
			if !inLiteral {
				inBasic = !inBasic
			}
		case '\'':
			if !inBasic {
				inLiteral = !inLiteral
			}
		case '#':
			if !inBasic && !inLiteral {
				return s[:i]
			}
		}
	}
	return s
}

func parseValue(text string, lineNo int) (tomlValue, error) {
	switch {
	case text == "":
		return tomlValue{}, fmt.Errorf("line %d: missing value", lineNo)
	case text == "true" || text == "false":
		return tomlValue{kind: tomlBool, b: text == "true", line: lineNo}, nil
	case strings.HasPrefix(text, "["):
		return parseArray(text, lineNo)
	case strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'"):
		s, err := parseString(text, lineNo)
		if err != nil {
			return tomlValue{}, err
		}
		return tomlValue{kind: tomlString, s: s, line: lineNo}, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return tomlValue{}, fmt.Errorf(
			"line %d: %q is not a value — laws use quoted strings, integers, true/false, or [\"a\", \"b\"]", lineNo, text)
	}
	return tomlValue{kind: tomlInt, i: n, line: lineNo}, nil
}

// parseString reads one basic ("…", with escapes) or literal ('…', verbatim)
// string occupying the whole of text.
func parseString(text string, lineNo int) (string, error) {
	s, rest, err := scanString(text, lineNo)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", fmt.Errorf("line %d: trailing text after a string: %q", lineNo, rest)
	}
	return s, nil
}

// scanString reads the string starting at text[0] and returns it with whatever
// follows the closing quote.
func scanString(text string, lineNo int) (value, rest string, err error) {
	quote := text[0]
	if quote == '\'' {
		end := strings.IndexByte(text[1:], '\'')
		if end < 0 {
			return "", "", fmt.Errorf("line %d: unterminated literal string %q", lineNo, text)
		}
		return text[1 : 1+end], text[2+end:], nil
	}
	var b strings.Builder
	for i := 1; i < len(text); i++ {
		c := text[i]
		if c == '\\' {
			if i+1 >= len(text) {
				return "", "", fmt.Errorf("line %d: trailing escape in %q", lineNo, text)
			}
			i++
			switch text[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				// A regex is the common case here: `\.`, `\(`, `\s` all pass
				// through as the two bytes the pattern needs.
				b.WriteByte('\\')
				b.WriteByte(text[i])
			}
			continue
		}
		if c == '"' {
			return b.String(), text[i+1:], nil
		}
		b.WriteByte(c)
	}
	return "", "", fmt.Errorf("line %d: unterminated string %q", lineNo, text)
}

func parseArray(text string, lineNo int) (tomlValue, error) {
	if !strings.HasSuffix(text, "]") {
		return tomlValue{}, fmt.Errorf("line %d: unterminated array %q — a law's array stays on one line", lineNo, text)
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	out := tomlValue{kind: tomlArray, list: []string{}, line: lineNo}
	for body != "" {
		if body[0] != '"' && body[0] != '\'' {
			return tomlValue{}, fmt.Errorf("line %d: array elements are quoted strings, got %q", lineNo, body)
		}
		s, rest, err := scanString(body, lineNo)
		if err != nil {
			return tomlValue{}, err
		}
		out.list = append(out.list, s)
		body = strings.TrimSpace(rest)
		if strings.HasPrefix(body, ",") {
			body = strings.TrimSpace(body[1:])
			continue
		}
		if body != "" {
			return tomlValue{}, fmt.Errorf("line %d: expected `,` between array elements, got %q", lineNo, body)
		}
	}
	return out, nil
}
