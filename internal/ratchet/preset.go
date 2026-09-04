package ratchet

import (
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Presets ships known-good law bodies as embedded data — the way a consuming
// repo starts a law family (`aphrollo ratchet init --preset common,rust`)
// instead of hand-copying one from another repo's `.ratchet/laws/`. A preset
// is a normal law TOML file, except a matcher field may carry a `{{name}}`
// slot: the value a local law's own `[params]` table supplies for it. Once
// written the local file is a fully self-contained, ordinary law — `extends`
// and `[params]` never change how it is scanned, only how `ratchet check`
// re-renders the preset to warn on drift.

//go:embed presets
var presetsFS embed.FS

const presetsRoot = "presets"

// PresetEntry names one embedded preset and the parameters its template asks
// for, in the order they first appear.
type PresetEntry struct {
	Group  string
	Name   string
	Params []string
}

// placeholderPattern matches a `{{name}}` template slot.
var placeholderPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// ListPresets returns every embedded preset, sorted by group then name.
func ListPresets() ([]PresetEntry, error) {
	groups, err := presetsFS.ReadDir(presetsRoot)
	if err != nil {
		return nil, fmt.Errorf("reading embedded presets: %w", err)
	}
	var out []PresetEntry
	for _, g := range groups {
		if !g.IsDir() {
			continue
		}
		files, err := presetsFS.ReadDir(path(presetsRoot, g.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded presets/%s: %w", g.Name(), err)
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".toml") {
				continue
			}
			name := strings.TrimSuffix(f.Name(), ".toml")
			raw, err := LoadPresetText(g.Name(), name)
			if err != nil {
				return nil, err
			}
			out = append(out, PresetEntry{Group: g.Name(), Name: name, Params: presetPlaceholders(raw)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// LoadPresetText reads one embedded preset's raw, unrendered TOML text.
func LoadPresetText(group, name string) (string, error) {
	data, err := presetsFS.ReadFile(path(path(presetsRoot, group), name+".toml"))
	if err != nil {
		return "", fmt.Errorf("no preset %s/%s", group, name)
	}
	return string(data), nil
}

// presetPlaceholders lists a template's distinct `{{name}}` slots, in the
// order they first appear.
func presetPlaceholders(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range placeholderPattern.FindAllStringSubmatch(raw, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// RenderPresetText substitutes every `{{name}}` slot with params[name],
// returning the rendered text and every slot params left unfilled (in the
// order presetPlaceholders lists them) — the caller decides whether a
// missing slot blocks the write or is just noted.
func RenderPresetText(raw string, params map[string]string) (rendered string, missing []string) {
	rendered = raw
	for _, name := range presetPlaceholders(raw) {
		v, ok := params[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		rendered = strings.ReplaceAll(rendered, "{{"+name+"}}", v)
	}
	return rendered, missing
}

// WithExtends is what `ratchet init` writes: rendered (the preset's text,
// already substituted) with an `extends = "preset:<group>/<name>"` line
// inserted right after `name = ...`, and a `[params]` table appended holding
// only the params THIS preset actually used — so a later `ratchet check` can
// re-render the same preset and compare, without guessing which of a
// multi-preset `init` run's `--param` flags belonged to this law.
func WithExtends(rendered, group, name string, params map[string]string, usedParamNames []string) string {
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines)+4)
	inserted := false
	for _, l := range lines {
		out = append(out, l)
		if !inserted && strings.HasPrefix(strings.TrimSpace(l), "name") {
			out = append(out, fmt.Sprintf("extends     = %q", "preset:"+group+"/"+name))
			inserted = true
		}
	}
	text := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	if len(usedParamNames) == 0 {
		return text
	}
	names := append([]string{}, usedParamNames...)
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n[params]\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s = %q\n", n, params[n])
	}
	return b.String()
}

// ParsePresetRef splits "preset:<group>/<name>" into its parts.
func ParsePresetRef(ref string) (group, name string, err error) {
	rest, ok := strings.CutPrefix(ref, "preset:")
	if !ok {
		return "", "", fmt.Errorf(`%q is not a preset reference — want "preset:<group>/<name>"`, ref)
	}
	group, name, ok = strings.Cut(rest, "/")
	if !ok || group == "" || name == "" {
		return "", "", fmt.Errorf(`%q is not a preset reference — want "preset:<group>/<name>"`, ref)
	}
	return group, name, nil
}

// presetDrift reports how a law that extends a preset has diverged from it:
// the preset re-rendered with the law's own Params, compared field-for-field
// against the law's actual [matcher] table. An empty string means no drift —
// either the law does not extend anything, or its matcher still matches.
func presetDrift(law Law) (string, error) {
	if law.Extends == "" {
		return "", nil
	}
	group, name, err := ParsePresetRef(law.Extends)
	if err != nil {
		return "", err
	}
	raw, err := LoadPresetText(group, name)
	if err != nil {
		return "", err
	}
	rendered, _ := RenderPresetText(raw, law.Params)
	presetDoc, err := parseTOML(rendered)
	if err != nil {
		return "", fmt.Errorf("preset %s/%s does not parse once rendered: %w", group, name, err)
	}
	localDoc, err := parseTOML(law.Source)
	if err != nil {
		return "", err
	}
	if canonicalMatcher(presetDoc) == canonicalMatcher(localDoc) {
		return "", nil
	}
	return fmt.Sprintf("%s: [matcher] differs from %s", law.Name, law.Extends), nil
}

// canonicalMatcher is an order-independent fingerprint of a law's [matcher]
// table, for comparing two TOML documents without a full AST diff.
func canonicalMatcher(doc *tomlDoc) string {
	return canonicalSection(doc, "matcher")
}

// canonicalSection is canonicalMatcher's shape generalized to any one TOML
// table: an order-independent `key=value;key=value` fingerprint.
func canonicalSection(doc *tomlDoc, section string) string {
	parts := make([]string, 0, len(doc.keys(section)))
	for _, k := range doc.keys(section) {
		v, _ := doc.value(section, k)
		parts = append(parts, k+"="+canonicalTOMLValue(v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// RuleSemantics fingerprints the fields of a law's TOML source that decide
// what it catches — [matcher], [scope], and severity — so two laws with the
// same RuleSemantics judge the tree identically no matter what their name,
// description, or comments say. It is --adopt's changed-since-HEAD guard:
// a byte diff of the whole file would let a description reword or a
// comment edit "change" a law that catches exactly what it always did.
func RuleSemantics(source string) (string, error) {
	doc, err := parseTOML(source)
	if err != nil {
		return "", err
	}
	return canonicalSection(doc, "matcher") + "\x00" +
		canonicalSection(doc, "scope") + "\x00severity=" + doc.str("", "severity"), nil
}

func canonicalTOMLValue(v tomlValue) string {
	switch v.kind {
	case tomlString:
		return v.s
	case tomlInt:
		return strconv.Itoa(v.i)
	case tomlBool:
		return strconv.FormatBool(v.b)
	case tomlArray:
		return strings.Join(v.list, ",")
	default:
		return ""
	}
}
