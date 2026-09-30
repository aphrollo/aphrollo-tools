// Package tomlsubset reads the TOML this binary's data files are written in: a
// language row, a ratchet law. The binary carries no TOML dependency, so the
// reader takes the subset those files need: root keys, `[table]` and
// `[table.id]` headers, and values that are a string, an integer, a boolean or
// an array of strings on one line. Anything else is an error, never ignored: a
// typo'd key must fail at load, not silently turn a feature off.
package tomlsubset

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind is the type of a value.
type Kind int

const (
	String Kind = iota
	Bool
	List
	Int
)

func (k Kind) String() string {
	switch k {
	case Bool:
		return "boolean"
	case List:
		return "array of strings"
	case Int:
		return "integer"
	default:
		return "string"
	}
}

// Value is one parsed value; only the field matching Kind is meaningful.
type Value struct {
	Kind Kind
	S    string
	B    bool
	N    int
	List []string
	Line int
}

// Table is one header's keys, in the order the file declared them. The root
// table has the empty name.
type Table struct {
	Name string
	Line int
	Keys map[string]Value
	Seen []string
}

// Document is a parsed file: the root table, then every header in file order.
type Document struct {
	Root   *Table
	Tables []*Table
}

// Section finds the table with exactly this name, or nil.
func (d *Document) Section(name string) *Table {
	for _, t := range d.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Subsections lists the tables named prefix.<id>, in file order.
func (d *Document) Subsections(prefix string) []*Table {
	var out []*Table
	for _, t := range d.Tables {
		if strings.HasPrefix(t.Name, prefix+".") {
			out = append(out, t)
		}
	}
	return out
}

// Parse reads a whole file. Errors name the line.
func Parse(text string) (*Document, error) {
	doc := &Document{Root: &Table{Keys: map[string]Value{}}}
	cur := doc.Root
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
			if doc.Section(name) != nil {
				return nil, fmt.Errorf("line %d: table [%s] is declared twice", lineNo, name)
			}
			cur = &Table{Name: name, Line: lineNo, Keys: map[string]Value{}}
			doc.Tables = append(doc.Tables, cur)
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
		if _, dup := cur.Keys[key]; dup {
			return nil, fmt.Errorf("line %d: key %q is set twice in the same table", lineNo, key)
		}
		val, err := readValue(strings.TrimSpace(dropComment(rest)), lineNo)
		if err != nil {
			return nil, err
		}
		cur.Keys[key] = val
		cur.Seen = append(cur.Seen, key)
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

func readValue(text string, lineNo int) (Value, error) {
	switch {
	case text == "":
		return Value{}, fmt.Errorf("line %d: missing value", lineNo)
	case text == "true" || text == "false":
		return Value{Kind: Bool, B: text == "true", Line: lineNo}, nil
	case strings.HasPrefix(text, "["):
		return readList(text, lineNo)
	case strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'"):
		s, rest, err := scanString(text, lineNo)
		if err != nil {
			return Value{}, err
		}
		if strings.TrimSpace(rest) != "" {
			return Value{}, fmt.Errorf("line %d: trailing text after a string: %q", lineNo, rest)
		}
		return Value{Kind: String, S: s, Line: lineNo}, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return Value{}, fmt.Errorf("line %d: %q is not a value — use a quoted string, an integer, true/false, or [\"a\", \"b\"]", lineNo, text)
	}
	return Value{Kind: Int, N: n, Line: lineNo}, nil
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

func readList(text string, lineNo int) (Value, error) {
	if !strings.HasSuffix(text, "]") {
		return Value{}, fmt.Errorf("line %d: unterminated array %q — an array stays on one line", lineNo, text)
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	out := Value{Kind: List, List: []string{}, Line: lineNo}
	// Each element consumes at least its opening quote, so the text's length
	// bounds the turns.
	for range len(text) {
		if body == "" {
			break
		}
		if body[0] != '"' && body[0] != '\'' {
			return Value{}, fmt.Errorf("line %d: array elements are quoted strings, got %q", lineNo, body)
		}
		s, rest, err := scanString(body, lineNo)
		if err != nil {
			return Value{}, err
		}
		out.List = append(out.List, s)
		body = strings.TrimSpace(rest)
		if after, ok := strings.CutPrefix(body, ","); ok {
			body = strings.TrimSpace(after)
			continue
		}
		if body != "" {
			return Value{}, fmt.Errorf("line %d: expected `,` between array elements, got %q", lineNo, body)
		}
	}
	return out, nil
}

// Fields reads a table's keys against a declared set: a key the set does not
// name is an error, and each getter checks the kind it was asked for.
type Fields struct {
	t    *Table
	file string
	err  error
	used map[string]bool
}

// NewFields starts reading t; file names the source in error messages.
func NewFields(t *Table, file string) *Fields {
	return &Fields{t: t, file: file, used: map[string]bool{}}
}

func (f *Fields) where(key string) string {
	name := f.t.Name
	if name == "" {
		name = "root"
	}
	return fmt.Sprintf("%s: [%s] %s", f.file, name, key)
}

func (f *Fields) get(key string, want Kind) (Value, bool) {
	f.used[key] = true
	v, ok := f.t.Keys[key]
	if !ok {
		return Value{}, false
	}
	if v.Kind != want && f.err == nil {
		f.err = fmt.Errorf("%s (line %d): expects a %s, not a %s", f.where(key), v.Line, want, v.Kind)
		return Value{}, false
	}
	return v, true
}

// Str is the string at key, "" when absent.
func (f *Fields) Str(key string) string {
	v, _ := f.get(key, String)
	return v.S
}

// Num is the integer at key, 0 when absent.
func (f *Fields) Num(key string) int {
	v, _ := f.get(key, Int)
	return v.N
}

// Flag is the boolean at key, false when absent.
func (f *Fields) Flag(key string) bool {
	v, _ := f.get(key, Bool)
	return v.B
}

// List is the array at key, nil when absent.
func (f *Fields) List(key string) []string {
	v, _ := f.get(key, List)
	return v.List
}

// Has reports whether the table sets key.
func (f *Fields) Has(key string) bool {
	_, ok := f.t.Keys[key]
	return ok
}

// Finish reports the first kind error, else the first key nobody asked for.
func (f *Fields) Finish() error {
	if f.err != nil {
		return f.err
	}
	var unknown []string
	for key := range f.t.Keys {
		if !f.used[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s (line %d): unknown key", f.where(unknown[0]), f.t.Keys[unknown[0]].Line)
}
