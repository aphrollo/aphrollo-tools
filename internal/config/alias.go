package config

import (
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/config/decl"
	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// Until a repo moves to trellis.toml, its [aphrollo] table in aphrollo.toml
// (or [workspace.metadata.aphrollo] in Cargo.toml) is an alias of the repo
// layer: each key that has a schema equivalent reads as a repo-layer
// declaration of that key, ranked below trellis.toml. A key with no equivalent
// stays readable under its own name (Legacy).

// Legacy is a key of aphrollo.toml or Cargo.toml read under its own name.
type Legacy struct {
	Name   string
	Value  tomlsubset.Value
	Source string
}

// Legacy lists the keys of the gate's tables that no schema key replaces, in
// file order, aphrollo.toml's first.
func (c *Config) Legacy() []Legacy { return c.legacy }

const (
	aphrolloFile = "aphrollo.toml"
	cargoFile    = "Cargo.toml"
	aphrolloTbl  = "aphrollo"
	cargoTbl     = "workspace.metadata.aphrollo"
)

// aliasSources maps each key of the gate's tables a schema key replaces to
// that key.
var aliasSources = map[string]string{
	"undercover":             "undercover",
	"ci":                     "ci",
	"requires":               "requires",
	"mutants-at-merge":       "mutation",
	"mutants-at-commit":      "mutation",
	"mutants-at-merge-level": "mutation",
	"retro-prompt":           "retro-prompt",
	"issue-prompt":           "issue-prompt",
	"report":                 "report",
}

// source is one file's table.
type source struct {
	name string
	t    *decl.Table
}

func (s source) alias(keys ...string) string { return s.name + ":" + strings.Join(keys, ",") }

func (r *reader) aliasFiles(repo string) {
	aph := source{aphrolloFile, decl.Read(filepath.Join(repo, aphrolloFile), aphrolloTbl)}
	cargo := source{cargoFile, decl.Read(filepath.Join(repo, cargoFile), cargoTbl)}
	for _, s := range []source{aph, cargo} {
		for _, b := range s.t.Bad {
			name, ok := aliasSources[b.Key]
			if !ok {
				continue // not a key the alias reads: the other readers' business
			}
			k, _ := Lookup(name)
			r.diag(s.t.Path, Repo, b.Line, k.Name, s.name+" "+b.Key+": "+b.Msg+"; using the built-in value")
			r.claims = append(r.claims, claim{key: k, layer: Repo, rank: rankAlias, file: s.t.Path, line: b.Line,
				invalid: true, reported: true, alias: s.alias(b.Key)})
		}
		for _, field := range s.t.Seen {
			if _, ok := aliasSources[field]; !ok {
				r.legacy = append(r.legacy, Legacy{Name: field, Value: s.t.Keys[field], Source: s.name})
			}
		}
	}
	r.aliasUndercover(aph, cargo)
	r.aliasRequires(aph, cargo)
	for _, name := range []string{"retro-prompt", "issue-prompt", "report"} {
		for _, s := range []source{cargo, aph} {
			if v, ok := s.t.Value(name); ok {
				r.aliasClaim(s, name, v)
			}
		}
	}
	if v, ok := aph.t.Value("ci"); ok {
		r.aliasClaim(aph, "ci", v)
	}
	// The mutation reader has always asked Cargo.toml's table first.
	r.aliasMutation(cargo, aph)
}

// aliasClaim validates one aliased value and records it as a repo-layer claim.
func (r *reader) aliasClaim(s source, name string, v tomlsubset.Value) {
	k, _ := Lookup(name)
	c := claim{key: k, layer: Repo, rank: rankAlias, file: s.t.Path, line: v.Line, alias: s.alias(name)}
	nv, err := k.Validate(v)
	c.value = nv
	if err != nil {
		c.invalid, c.reported = true, true
		r.diag(s.t.Path, Repo, v.Line, k.Name, s.name+" "+name+": "+err.Error()+"; using the built-in value")
	}
	r.claims = append(r.claims, c)
}

// aliasUndercover is on when either file says so: the commit-msg gate has
// always read both tables.
func (r *reader) aliasUndercover(srcs ...source) {
	k, _ := Lookup("undercover")
	var from []string
	c := claim{key: k, layer: Repo, rank: rankAlias, value: flag(false)}
	for _, s := range srcs {
		v, ok := s.t.Value("undercover")
		if !ok {
			continue
		}
		from = append(from, s.alias("undercover"))
		if c.file == "" {
			c.file, c.line = s.t.Path, v.Line
		}
		nv, err := k.Validate(v)
		if err != nil {
			c.invalid, c.reported = true, true
			r.diag(s.t.Path, Repo, v.Line, k.Name, s.name+" undercover: "+err.Error()+"; using the built-in value")
			continue
		}
		if nv.B {
			c.value = flag(true)
		}
	}
	if len(from) > 0 {
		c.alias = strings.Join(from, " ")
		r.claims = append(r.claims, c)
	}
}

// aliasRequires is the strictest floor either file declares: the version guard
// has always compared both.
func (r *reader) aliasRequires(srcs ...source) {
	k, _ := Lookup("requires")
	var from []string
	c := claim{key: k, layer: Repo, rank: rankAlias}
	var strictest compat.Requirement
	have := false
	for _, s := range srcs {
		v, ok := s.t.Value("requires")
		if !ok {
			continue
		}
		from = append(from, s.alias("requires"))
		if c.file == "" {
			c.file, c.line = s.t.Path, v.Line
		}
		nv, err := k.Validate(v)
		if err != nil {
			c.invalid, c.reported = true, true
			r.diag(s.t.Path, Repo, v.Line, k.Name, s.name+" requires: "+err.Error()+"; using the built-in value")
			continue
		}
		req, _ := compat.ParseRequires(nv.S)
		if !have || strictest.Min.Less(req.Min) {
			strictest, have, c.value = req, true, nv
		}
	}
	if len(from) > 0 {
		c.alias = strings.Join(from, " ")
		r.claims = append(r.claims, c)
	}
}

// aliasMutation derives the mutation level the old keys amount to: block when
// a commit-time survivor refuses the commit, or when a merge measurement that
// is on pins its level to block; guide when a measurement is on at all; off
// otherwise. Each key is read from the first source that declares it.
func (r *reader) aliasMutation(srcs ...source) {
	k, _ := Lookup("mutation")
	c := claim{key: k, layer: Repo, rank: rankAlias}
	byFile := map[string][]string{}
	var order []string
	first := func(key string) (tomlsubset.Value, source, bool) {
		for _, s := range srcs {
			if v, ok := s.t.Value(key); ok {
				if byFile[s.name] == nil {
					order = append(order, s.name)
				}
				byFile[s.name] = append(byFile[s.name], key)
				if c.file == "" || v.Line < c.line && s.t.Path == c.file {
					c.file, c.line = s.t.Path, v.Line
				}
				return v, s, true
			}
		}
		return tomlsubset.Value{}, source{}, false
	}
	fail := func(s source, key string, v tomlsubset.Value, want string) {
		c.invalid, c.reported, c.line, c.file = true, true, v.Line, s.t.Path
		r.diag(s.t.Path, Repo, v.Line, k.Name, s.name+" "+key+": expects "+want+"; using the built-in value")
	}
	var mergeOn, commitOn, commitBlock, levelBlock bool
	if v, s, ok := first("mutants-at-merge"); ok {
		switch word(v) {
		case "true", "ci":
			mergeOn = true
		case "false":
		default:
			fail(s, "mutants-at-merge", v, `true, false or "ci"`)
		}
	}
	if v, s, ok := first("mutants-at-commit"); ok && !c.invalid {
		switch word(v) {
		case "true", "report":
			commitOn = true
		case "block":
			commitOn, commitBlock = true, true
		case "false":
		default:
			fail(s, "mutants-at-commit", v, `true, false, "report" or "block"`)
		}
	}
	if v, s, ok := first("mutants-at-merge-level"); ok && !c.invalid {
		switch word(v) {
		case "block":
			levelBlock = true
		case "report":
		default:
			fail(s, "mutants-at-merge-level", v, `"report" or "block"`)
		}
	}
	if len(order) == 0 {
		return
	}
	var parts []string
	for _, name := range order {
		parts = append(parts, name+":"+strings.Join(byFile[name], ","))
	}
	c.alias = strings.Join(parts, " ")
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
