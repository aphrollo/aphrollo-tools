package config

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// Until the readers of aphrollo.toml move to the schema, its [aphrollo] table
// is an alias of the repo layer: each key that has a schema equivalent reads
// as a repo-layer declaration of that key, ranked below trellis.toml. A key
// with no equivalent stays readable under its own name (Legacy).

// Legacy is an aphrollo.toml key read under its own name.
type Legacy struct {
	Name  string
	Value tomlsubset.Value
}

// Legacy lists the [aphrollo] keys of aphrollo.toml that no schema key
// replaces, in file order.
func (c *Config) Legacy() []Legacy { return c.legacy }

const aliasFileName = "aphrollo.toml"

// aliasSources maps each aphrollo.toml key a schema key replaces to that key.
var aliasSources = map[string]string{
	"undercover":             "undercover",
	"ci":                     "ci",
	"requires":               "requires",
	"mutants-at-merge":       "mutation",
	"mutants-at-commit":      "mutation",
	"mutants-at-merge-level": "mutation",
}

// mutationSources are the keys the mutation level is derived from, in the
// order an alias names them.
var mutationSources = []string{"mutants-at-merge", "mutants-at-commit", "mutants-at-merge-level"}

func (r *reader) aliasFile(path string) {
	onBad := func(n int, line, msg string) {
		field, _, _ := strings.Cut(line, "=")
		field = strings.TrimSpace(field)
		name, ok := aliasSources[field]
		if !ok {
			return // not a key this file's alias reads: the old readers' business
		}
		k, _ := Lookup(name)
		r.diag(path, Repo, n, k.Name, aliasFileName+" "+field+": "+msg+"; using the built-in value")
		r.claims = append(r.claims, claim{key: k, layer: Repo, rank: rankAlias, file: path, line: n,
			invalid: true, reported: true, alias: aliasFileName + ":" + field})
	}
	doc := r.parse(path, Repo, collapseArrays, onBad)
	if doc == nil {
		return
	}
	t := doc.Section("aphrollo")
	if t == nil {
		return
	}
	for _, field := range t.Seen {
		if _, ok := aliasSources[field]; !ok {
			r.legacy = append(r.legacy, Legacy{Name: field, Value: t.Keys[field]})
		}
	}
	for _, name := range []string{"undercover", "ci", "requires"} {
		v, ok := t.Keys[name]
		if !ok {
			continue
		}
		r.aliasClaim(path, name, []string{name}, v)
	}
	r.aliasMutation(path, t)
}

// aliasClaim validates one aliased value and records it as a repo-layer claim.
func (r *reader) aliasClaim(path, name string, from []string, v tomlsubset.Value) {
	k, _ := Lookup(name)
	c := claim{key: k, layer: Repo, rank: rankAlias, file: path, line: v.Line, alias: aliasFileName + ":" + strings.Join(from, ",")}
	nv, err := k.Validate(v)
	c.value = nv
	if err != nil {
		c.invalid, c.reported = true, true
		r.diag(path, Repo, v.Line, k.Name, aliasFileName+" "+strings.Join(from, ",")+": "+err.Error()+"; using the built-in value")
	}
	r.claims = append(r.claims, c)
}

// aliasMutation derives the mutation level the old keys amount to: block when
// a commit-time survivor refuses the commit, or when a merge measurement that
// is on pins its level to block; guide when a measurement is on at all; off
// otherwise.
func (r *reader) aliasMutation(path string, t *tomlsubset.Table) {
	var present []string
	line := 0
	for _, s := range mutationSources {
		if v, ok := t.Keys[s]; ok {
			present = append(present, s)
			if line == 0 || v.Line < line {
				line = v.Line
			}
		}
	}
	if len(present) == 0 {
		return
	}
	k, _ := Lookup("mutation")
	c := claim{key: k, layer: Repo, rank: rankAlias, file: path, line: line, alias: aliasFileName + ":" + strings.Join(present, ",")}
	fail := func(src string, v tomlsubset.Value, want string) {
		c.invalid, c.reported, c.line = true, true, v.Line
		r.diag(path, Repo, v.Line, k.Name, aliasFileName+" "+src+": expects "+want+"; using the built-in value")
	}
	var mergeOn, commitOn, commitBlock, levelBlock bool
	if v, ok := t.Keys["mutants-at-merge"]; ok {
		switch word(v) {
		case "true", "ci":
			mergeOn = true
		case "false":
		default:
			fail("mutants-at-merge", v, `true, false or "ci"`)
		}
	}
	if v, ok := t.Keys["mutants-at-commit"]; ok && !c.invalid {
		switch word(v) {
		case "true", "report":
			commitOn = true
		case "block":
			commitOn, commitBlock = true, true
		case "false":
		default:
			fail("mutants-at-commit", v, `true, false, "report" or "block"`)
		}
	}
	if v, ok := t.Keys["mutants-at-merge-level"]; ok && !c.invalid {
		switch word(v) {
		case "block":
			levelBlock = true
		case "report":
		default:
			fail("mutants-at-merge-level", v, `"report" or "block"`)
		}
	}
	switch {
	case commitBlock || (mergeOn && levelBlock):
		c.value = str("block")
	case mergeOn || commitOn:
		c.value = str("guide")
	default:
		c.value = str("off")
	}
	r.claims = append(r.claims, c)
}

// word is a value as the old line readers saw it: a boolean or a string,
// quotes and case aside.
func word(v tomlsubset.Value) string {
	switch v.Kind {
	case tomlsubset.Bool:
		if v.B {
			return "true"
		}
		return "false"
	case tomlsubset.String:
		return strings.TrimSpace(v.S)
	}
	return "?"
}

// collapseArrays puts each multi-line array on the line its key starts on,
// and leaves blank lines where the rest of it was, so every line number is the
// file's own. aphrollo.toml spells its long lists across lines; the table
// reader takes an array on one line.
func collapseArrays(text string) string {
	lines := strings.Split(text, "\n")
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
