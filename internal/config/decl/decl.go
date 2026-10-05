// Package decl reads the keys a repo declares for the gate in one table of a
// TOML file: [aphrollo] in aphrollo.toml, [workspace.metadata.aphrollo] in
// Cargo.toml. It is the one reader of those tables, built on tomlsubset: a
// trailing comment is not part of a value, an array may span lines, and a line
// the reader refuses is named and set aside without costing the keys beside it.
// It imports nothing of the gate, so any package may read through it.
package decl

import (
	"os"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// Bad is a line of the table the reader refused.
type Bad struct {
	Line int
	Key  string
	Msg  string
}

// Table is what one table of one file declares.
type Table struct {
	Path string
	// Header is whether the table's header is in the file at all, keys or not.
	Header bool
	Keys   map[string]tomlsubset.Value
	// Seen lists the readable keys in file order.
	Seen []string
	Bad  []Bad

	written map[string]bool
	// rawBad is the text of a value the reader refused, comment cut off.
	rawBad map[string]string
}

// Read reads table (a name without brackets, "aphrollo" or
// "workspace.metadata.aphrollo") of the file at path. A file or table that is
// not there reads empty.
func Read(path, table string) *Table {
	t := &Table{Path: path, Keys: map[string]tomlsubset.Value{}, written: map[string]bool{}, rawBad: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return t
	}
	lines := strings.Split(Collapse(string(data)), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "["+table+"]" {
			start = i
			break
		}
	}
	if start < 0 {
		return t
	}
	t.Header = true
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			end = i
			break
		}
	}
	// The region keeps its own line numbers: everything above it is blank.
	region := make([]string, 0, end)
	for range start + 1 {
		region = append(region, "")
	}
	region = append(region, lines[start+1:end]...)
	for range len(region) + 1 {
		doc, err := tomlsubset.Parse(strings.Join(region, "\n"))
		if err == nil {
			t.Keys, t.Seen = doc.Root.Keys, doc.Root.Seen
			for _, k := range t.Seen {
				t.written[k] = true
			}
			return t
		}
		n := errLine(err.Error())
		if n < 1 || n > len(region) {
			t.Bad = append(t.Bad, Bad{Msg: err.Error()})
			return t
		}
		key, rhs, _ := strings.Cut(region[n-1], "=")
		key = strings.TrimSpace(key)
		t.written[key] = true
		_, cut := scanBrackets(rhs, 0)
		if _, dup := t.rawBad[key]; !dup && !t.hasKeyBefore(key) {
			t.rawBad[key] = strings.TrimSpace(rhs[:cut])
		}
		t.Bad = append(t.Bad, Bad{Line: n, Key: key, Msg: strings.TrimPrefix(err.Error(), "line "+strconv.Itoa(n)+": ")})
		region[n-1] = ""
	}
	return t
}

func errLine(msg string) int {
	rest, ok := strings.CutPrefix(msg, "line ")
	if !ok {
		return 0
	}
	num, _, _ := strings.Cut(rest, ":")
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0
	}
	return n
}

// Written reports whether the key is on the page, whatever its value reads as.
func (t *Table) Written(key string) bool { return t.written[key] }

func (t *Table) hasKeyBefore(key string) bool {
	_, ok := t.Keys[key]
	return ok
}

// Value is the key's parsed value.
func (t *Table) Value(key string) (tomlsubset.Value, bool) {
	v, ok := t.Keys[key]
	return v, ok
}

// Raw is the key as the gate's readers take a scalar: a string as it is, a
// boolean or a number as its text, an array in its one-line spelling.
func (t *Table) Raw(key string) (string, bool) {
	v, ok := t.Keys[key]
	if !ok {
		// A value the reader refused is still what was written: the caller that
		// refuses a bad number needs its text.
		if raw, bad := t.rawBad[key]; bad {
			return strings.Trim(raw, `"`), true
		}
		return "", false
	}
	switch v.Kind {
	case tomlsubset.Bool:
		return strconv.FormatBool(v.B), true
	case tomlsubset.Int:
		return strconv.Itoa(v.N), true
	case tomlsubset.List:
		parts := make([]string, len(v.List))
		for i, s := range v.List {
			parts[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	}
	return v.S, true
}

// Flag is a boolean key: true only for a boolean true, and whether the key
// was written at all. A default that is not false needs the second answer.
func (t *Table) Flag(key string) (value, set bool) {
	v, ok := t.Keys[key]
	return ok && v.Kind == tomlsubset.Bool && v.B, t.written[key]
}

// List is an array of strings in file order; a lone string reads as a list of
// one. nil when the key is absent.
func (t *Table) List(key string) []string {
	v, ok := t.Keys[key]
	switch {
	case !ok:
		return nil
	case v.Kind == tomlsubset.List:
		return v.List
	case v.Kind == tomlsubset.String:
		return []string{v.S}
	}
	return nil
}

// Collapse puts each multi-line array on the line its key starts on and leaves
// blank lines where the rest of it was, so every line number is the file's own.
// aphrollo.toml spells its long lists across lines; tomlsubset takes an array
// on one line.
func Collapse(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		depth, cut := scanBrackets(line, 0)
		if trimmed == "" || trimmed[0] == '[' || trimmed[0] == '#' || !strings.Contains(line, "=") || depth <= 0 {
			out = append(out, line)
			continue
		}
		joined := strings.TrimRight(line[:cut], " \t\r")
		j := i
		for depth > 0 && j+1 < len(lines) {
			j++
			var next int
			depth, next = scanBrackets(lines[j], depth)
			if part := strings.TrimSpace(lines[j][:next]); part != "" {
				joined += " " + part
			}
		}
		out = append(out, joined)
		for k := i + 1; k <= j; k++ {
			out = append(out, "")
		}
		i = j
	}
	return strings.Join(out, "\n")
}

// scanBrackets walks one line from bracket depth start, ignoring brackets in
// strings, and answers the depth at its end and where its comment starts (the
// line's length when it has none).
func scanBrackets(line string, start int) (depth, cut int) {
	depth = start
	basic, literal, escaped := false, false, false
	for i := range len(line) {
		c := line[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && basic:
			escaped = true
		case c == '"' && !literal:
			basic = !basic
		case c == '\'' && !basic:
			literal = !literal
		case basic || literal:
		case c == '#':
			return depth, i
		case c == '[':
			depth++
		case c == ']':
			depth--
		}
	}
	return depth, len(line)
}
