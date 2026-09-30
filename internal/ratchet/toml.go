package ratchet

import (
	"fmt"
	"strings"

	toml "github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// The law files are TOML, read by the one subset reader of internal/tomlsubset.
// A law's shape is narrower than a language row's: root keys plus ONE level of
// `[table]`, so a dotted header is rejected here. Rejecting rather than
// ignoring is the point — a law is a rule other people rely on, so a typo'd
// key must fail loudly at load, not silently disable half the rule.

type tomlKind = toml.Kind

const (
	tomlString = toml.String
	tomlInt    = toml.Int
	tomlBool   = toml.Bool
	tomlArray  = toml.List
)

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
	names    []string
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

// sectionNames lists every table name declared, in file order, root ("")
// excluded.
func (d *tomlDoc) sectionNames() []string { return d.names }

func parseTOML(text string) (*tomlDoc, error) {
	parsed, err := toml.Parse(text)
	if err != nil {
		return nil, err
	}
	doc := &tomlDoc{
		sections: map[string]map[string]tomlValue{},
		order:    map[string][]string{},
	}
	add := func(t *toml.Table) {
		vals := make(map[string]tomlValue, len(t.Keys))
		for key, v := range t.Keys {
			vals[key] = tomlValue{kind: v.Kind, s: v.S, i: v.N, b: v.B, list: v.List, line: v.Line}
		}
		doc.sections[t.Name] = vals
		doc.order[t.Name] = t.Seen
	}
	add(parsed.Root)
	for _, t := range parsed.Tables {
		// Only ONE level is legal: a law's shape is flat by design, and
		// `[matcher.extra]` would be a rule half of the engine never reads.
		if strings.Contains(t.Name, ".") {
			return nil, fmt.Errorf("line %d: nested table [%s] — a law file is one level deep", t.Line, t.Name)
		}
		add(t)
		doc.names = append(doc.names, t.Name)
	}
	return doc, nil
}
