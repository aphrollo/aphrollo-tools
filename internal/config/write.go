package config

import (
	"fmt"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// SetText returns text, the content of a trellis.toml or a user config.toml,
// with key set to v where the file spells it: a line that already sets the key
// is replaced in place, a new root key follows the last root key, a new table
// key follows the last key of its table, and a table the file lacks is added at
// the end. Everything else is kept byte for byte. A file the reader cannot
// parse is refused: rewriting it would hide what is wrong with it.
func SetText(text string, k Key, v tomlsubset.Value) (string, error) {
	doc, err := tomlsubset.Parse(normalizeUser(text))
	if err != nil {
		return "", fmt.Errorf("the file cannot be read, so it is not rewritten: %w", err)
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	body := strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(body, "\n")
	}
	entry := k.Field + " = " + TOML(v)

	t := doc.Root
	if k.Table != "" {
		t = doc.Section(k.Table)
	}
	switch {
	case t != nil && hasKey(t, k.Field):
		lines[t.Keys[k.Field].Line-1] = entry
	case t == nil:
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "["+k.Table+"]", entry)
	default:
		at := t.Line // the header's line, 0 for the root
		for _, f := range t.Seen {
			at = max(at, t.Keys[f].Line)
		}
		lines = insertAt(lines, at, entry)
		if k.Table == "" && at == 0 && len(lines) > 1 {
			// A root key at the very top of a file that opens with a table.
			lines = insertAt(lines, 1, "")
		}
	}
	return strings.Join(lines, eol) + eol, nil
}

func hasKey(t *tomlsubset.Table, field string) bool {
	_, ok := t.Keys[field]
	return ok
}

// insertAt puts line after the n-th line (n = 0 is the top).
func insertAt(lines []string, n int, line string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:n]...)
	out = append(out, line)
	return append(out, lines[n:]...)
}
