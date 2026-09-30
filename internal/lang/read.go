package lang

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A language row is TOML, and this binary carries no TOML dependency, so the
// reader takes the subset a row needs: root keys, `[table]` and `[table.id]`
// headers, and values that are a string, a boolean or an array of strings on
// one line. Anything else is an error, never ignored: a typo'd key must fail
// at load, not silently turn a language feature off.

type kind int

const (
	kindString kind = iota
	kindBool
	kindList
	kindInt
)

func (k kind) String() string {
	switch k {
	case kindBool:
		return "boolean"
	case kindList:
		return "array of strings"
	case kindInt:
		return "integer"
	default:
		return "string"
	}
}

type value struct {
	kind kind
	s    string
	b    bool
	n    int
	list []string
	line int
}

// table is one header's keys, in the order the file declared them.
type table struct {
	name string
	line int
	keys map[string]value
	seen []string
}

// document is a parsed file: the root table, then every header in file order.
type document struct {
	root   *table
	tables []*table
}

func (d *document) section(name string) *table {
	for _, t := range d.tables {
		if t.name == name {
			return t
		}
	}
	return nil
}

// subsections lists the tables named prefix.<id>, in file order.
func (d *document) subsections(prefix string) []*table {
	var out []*table
	for _, t := range d.tables {
		if strings.HasPrefix(t.name, prefix+".") {
			out = append(out, t)
		}
	}
	return out
}

func readDocument(text string) (*document, error) {
	doc := &document{root: &table{keys: map[string]value{}}}
	cur := doc.root
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name, err := headerName(line, lineNo)
			if err != nil {
				return nil, err
			}
			if doc.section(name) != nil {
				return nil, fmt.Errorf("line %d: table [%s] is declared twice", lineNo, name)
			}
			cur = &table{name: name, line: lineNo, keys: map[string]value{}}
			doc.tables = append(doc.tables, cur)
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected `key = value`, got %q", lineNo, line)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\"'[].") {
			return nil, fmt.Errorf("line %d: %q is not a bare key", lineNo, key)
		}
		if _, dup := cur.keys[key]; dup {
			return nil, fmt.Errorf("line %d: key %q is set twice in the same table", lineNo, key)
		}
		val, err := readValue(strings.TrimSpace(dropComment(rest)), lineNo)
		if err != nil {
			return nil, err
		}
		cur.keys[key] = val
		cur.seen = append(cur.seen, key)
	}
	return doc, nil
}

func headerName(line string, lineNo int) (string, error) {
	if !strings.HasSuffix(line, "]") {
		return "", fmt.Errorf("line %d: unterminated table header %q", lineNo, line)
	}
	name := strings.TrimSpace(line[1 : len(line)-1])
	if name == "" || strings.ContainsAny(name, " \t\"'[]") || strings.HasPrefix(name, ".") ||
		strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return "", fmt.Errorf("line %d: %q is not a bare table name", lineNo, line)
	}
	return name, nil
}

// dropComment removes a trailing `#` comment that starts outside a string.
func dropComment(s string) string {
	inBasic, inLiteral, escaped := false, false, false
	for i, c := range []byte(s) {
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inBasic:
			escaped = true
		case c == '"' && !inLiteral:
			inBasic = !inBasic
		case c == '\'' && !inBasic:
			inLiteral = !inLiteral
		case c == '#' && !inBasic && !inLiteral:
			return s[:i]
		}
	}
	return s
}

func readValue(text string, lineNo int) (value, error) {
	switch {
	case text == "":
		return value{}, fmt.Errorf("line %d: missing value", lineNo)
	case text == "true" || text == "false":
		return value{kind: kindBool, b: text == "true", line: lineNo}, nil
	case strings.HasPrefix(text, "["):
		return readList(text, lineNo)
	case strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'"):
		s, rest, err := scanString(text, lineNo)
		if err != nil {
			return value{}, err
		}
		if strings.TrimSpace(rest) != "" {
			return value{}, fmt.Errorf("line %d: trailing text after a string: %q", lineNo, rest)
		}
		return value{kind: kindString, s: s, line: lineNo}, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return value{}, fmt.Errorf("line %d: %q is not a value — use a quoted string, an integer, true/false, or [\"a\", \"b\"]", lineNo, text)
	}
	return value{kind: kindInt, n: n, line: lineNo}, nil
}

// scanString reads the string that opens text and returns it with whatever
// follows its closing quote. A basic string decodes `\n`, `\t`, `\r`, `\"`,
// `\\`; every other backslash pair passes through as the two bytes, so a
// regular expression needs no doubled backslashes inside a literal string.
func scanString(text string, lineNo int) (val, rest string, err error) {
	if text[0] == '\'' {
		end := strings.IndexByte(text[1:], '\'')
		if end < 0 {
			return "", "", fmt.Errorf("line %d: unterminated literal string %q", lineNo, text)
		}
		return text[1 : 1+end], text[2+end:], nil
	}
	var b strings.Builder
	escaped := false
	for i, c := range []byte(text) {
		if i == 0 {
			continue
		}
		switch {
		case escaped:
			escaped = false
			switch c {
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
				b.WriteByte('\\')
				b.WriteByte(c)
			}
		case c == '\\':
			escaped = true
		case c == '"':
			return b.String(), text[i+1:], nil
		default:
			b.WriteByte(c)
		}
	}
	return "", "", fmt.Errorf("line %d: unterminated string %q", lineNo, text)
}

func readList(text string, lineNo int) (value, error) {
	if !strings.HasSuffix(text, "]") {
		return value{}, fmt.Errorf("line %d: unterminated array %q — an array stays on one line", lineNo, text)
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	out := value{kind: kindList, list: []string{}, line: lineNo}
	// Each element consumes at least its opening quote, so the text's length
	// bounds the turns.
	for range len(text) {
		if body == "" {
			break
		}
		if body[0] != '"' && body[0] != '\'' {
			return value{}, fmt.Errorf("line %d: array elements are quoted strings, got %q", lineNo, body)
		}
		s, rest, err := scanString(body, lineNo)
		if err != nil {
			return value{}, err
		}
		out.list = append(out.list, s)
		body = strings.TrimSpace(rest)
		if after, ok := strings.CutPrefix(body, ","); ok {
			body = strings.TrimSpace(after)
			continue
		}
		if body != "" {
			return value{}, fmt.Errorf("line %d: expected `,` between array elements, got %q", lineNo, body)
		}
	}
	return out, nil
}

// fields reads a table's keys against a declared set: a key the set does not
// name is an error, and each getter checks the kind it was asked for.
type fields struct {
	t    *table
	file string
	err  error
	used map[string]bool
}

func newFields(t *table, file string) *fields {
	return &fields{t: t, file: file, used: map[string]bool{}}
}

func (f *fields) where(key string) string {
	name := f.t.name
	if name == "" {
		name = "root"
	}
	return fmt.Sprintf("%s: [%s] %s", f.file, name, key)
}

func (f *fields) get(key string, want kind) (value, bool) {
	f.used[key] = true
	v, ok := f.t.keys[key]
	if !ok {
		return value{}, false
	}
	if v.kind != want && f.err == nil {
		f.err = fmt.Errorf("%s (line %d): expects a %s, not a %s", f.where(key), v.line, want, v.kind)
		return value{}, false
	}
	return v, true
}

func (f *fields) str(key string) string {
	v, _ := f.get(key, kindString)
	return v.s
}

func (f *fields) num(key string) int {
	v, _ := f.get(key, kindInt)
	return v.n
}

func (f *fields) flag(key string) bool {
	v, _ := f.get(key, kindBool)
	return v.b
}

func (f *fields) list(key string) []string {
	v, _ := f.get(key, kindList)
	return v.list
}

func (f *fields) has(key string) bool {
	_, ok := f.t.keys[key]
	return ok
}

// finish reports the first kind error, else the first key nobody asked for.
func (f *fields) finish() error {
	if f.err != nil {
		return f.err
	}
	var unknown []string
	for key := range f.t.keys {
		if !f.used[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s (line %d): unknown key", f.where(unknown[0]), f.t.keys[unknown[0]].line)
}
