// Package config is the one schema of the settings a repo and a user declare,
// and the layers they are read through: built-in, then the user's config.toml
// (with a [repo."<id>"] section per repo), then the repo's trellis.toml, then a
// per-command flag. A bad key or value is named with its file, layer and line,
// and its layer falls back to the built-in value, never to off.
package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// Layer is where a setting's value came from.
type Layer int

const (
	BuiltIn Layer = iota
	User
	Repo
	// Env is a deprecated APHROLLO_* variable standing in for a key: above every
	// file, below a flag.
	Env
	Flag
)

func (l Layer) String() string {
	switch l {
	case User:
		return "user"
	case Repo:
		return "repo"
	case Env:
		return "env"
	case Flag:
		return "flag"
	default:
		return "built-in"
	}
}

// allowed is the set of file layers a key may be declared in.
type allowed uint8

const (
	inUser allowed = 1 << iota
	inRepo
	inAll = inUser | inRepo
)

func (a allowed) has(l Layer) bool {
	switch l {
	case User:
		return a&inUser != 0
	case Repo:
		return a&inRepo != 0
	}
	return true
}

// Key is one setting: its name, its type, its default, the layers that may
// declare it, and where a file spells it.
type Key struct {
	Name    string
	Kind    tomlsubset.Kind
	Enum    []string
	Min     int
	Default tomlsubset.Value
	Layers  allowed
	// Table and Field are the key's spelling in a file: a root key has the
	// empty Table. A dotted name that would collide with a scalar of the same
	// stem (ci, ci.os) is spelled with a hyphen, so the file stays valid TOML.
	Table, Field string
	// Check is a further test of a value of the right kind.
	Check func(string) error
}

// The built-in of tdd is warn, the architecture's default until the A/B decides: a lane no
// layer pins runs the arm tddarm assigns it, and trunk, which has no lane, runs this.
const tddBuiltIn = "warn"

func str(s string) tomlsubset.Value { return tomlsubset.Value{Kind: tomlsubset.String, S: s} }
func flag(b bool) tomlsubset.Value  { return tomlsubset.Value{Kind: tomlsubset.Bool, B: b} }
func num(n int) tomlsubset.Value    { return tomlsubset.Value{Kind: tomlsubset.Int, N: n} }
func list(s ...string) tomlsubset.Value {
	return tomlsubset.Value{Kind: tomlsubset.List, List: append([]string{}, s...)}
}

var schema = []Key{
	{Name: "tdd", Kind: tomlsubset.String, Enum: []string{"enforce", "warn", "off"}, Default: str(tddBuiltIn), Layers: inAll, Field: "tdd"},
	{Name: "isolation", Kind: tomlsubset.Bool, Default: flag(true), Layers: inAll, Field: "isolation"},
	{Name: "ci", Kind: tomlsubset.String, Enum: []string{"auto", "local", "github"}, Default: str("auto"), Layers: inAll, Field: "ci"},
	{Name: "ci.os", Kind: tomlsubset.List, Default: list("linux"), Layers: inRepo, Field: "ci-os"},
	{Name: "mutation", Kind: tomlsubset.String, Enum: []string{"off", "guide", "block"}, Default: str("off"), Layers: inRepo, Field: "mutation"},
	{Name: "requires", Kind: tomlsubset.String, Default: str(""), Layers: inRepo, Field: "requires", Check: func(s string) error {
		_, err := compat.ParseRequires(s)
		return err
	}},
	{Name: "undercover", Kind: tomlsubset.Bool, Default: flag(false), Layers: inRepo, Field: "undercover"},
	{Name: "trunk", Kind: tomlsubset.String, Default: str(""), Layers: inRepo, Field: "trunk"},
	{Name: "host.production", Kind: tomlsubset.Bool, Default: flag(false), Layers: inUser, Table: "host", Field: "production"},
	{Name: "pin", Kind: tomlsubset.String, Default: str(""), Layers: inUser, Field: "pin", Check: func(s string) error {
		_, err := compat.ParseVersion(s)
		return err
	}},
	{Name: "budgets.commit_s", Kind: tomlsubset.Int, Min: 1, Default: num(60), Layers: inRepo, Table: "budgets", Field: "commit_s"},
	{Name: "budgets.merge_s", Kind: tomlsubset.Int, Min: 1, Default: num(300), Layers: inRepo, Table: "budgets", Field: "merge_s"},
	// The settings of a box, not of a repo: each was an APHROLLO_* variable. Their
	// consumers floor or default a value they cannot use, as they did the variable,
	// so a number is not range-checked here.
	{Name: "budgets.edit_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(110), Layers: inUser, Table: "budgets", Field: "edit_s"},
	{Name: "budgets.lock_wait_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(1200), Layers: inUser, Table: "budgets", Field: "lock_wait_s"},
	{Name: "budgets.cargo_wait_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(1200), Layers: inUser, Table: "budgets", Field: "cargo_wait_s"},
	{Name: "budgets.git_wait_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(1200), Layers: inUser, Table: "budgets", Field: "git_wait_s"},
	{Name: "budgets.lint_wait_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(300), Layers: inUser, Table: "budgets", Field: "lint_wait_s"},
	{Name: "budgets.deferred_max_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(600), Layers: inUser, Table: "budgets", Field: "deferred_max_s"},
	// budgets.mech_total_s bounds all the runs of one split `go test` list together.
	{Name: "budgets.mech_total_s", Kind: tomlsubset.Int, Min: anyInt, Default: num(2700), Layers: inUser, Table: "budgets", Field: "mech_total_s"},
	// box.build_slots defaults to two: two sessions building into one target dir at half
	// jobs each, the observed sweet spot between one session at a time and the
	// link-wave OOM an uncapped free-for-all produced.
	{Name: "box.build_slots", Kind: tomlsubset.Int, Min: anyInt, Default: num(2), Layers: inUser, Table: "box", Field: "build_slots"},
	{Name: "box.mech_parallel", Kind: tomlsubset.Int, Min: anyInt, Default: num(0), Layers: inUser, Table: "box", Field: "mech_parallel"},
	{Name: "reply_style", Kind: tomlsubset.String, Enum: []string{"terse", "plain"}, Default: str("terse"), Layers: inUser, Field: "reply_style"},
	{Name: "test.reads", Kind: tomlsubset.List, Default: list(), Layers: inRepo, Table: "test", Field: "reads"},
	{Name: "test.slow_tag", Kind: tomlsubset.String, Default: str(""), Layers: inRepo, Table: "test", Field: "slow_tag"},
	// The two lines the gate injects into a session unasked: the post-merge retro
	// questions and the session-start open-issues line. Off until a repo or user opts in.
	{Name: "retro-prompt", Kind: tomlsubset.Bool, Default: flag(false), Layers: inAll, Field: "retro-prompt"},
	{Name: "issue-prompt", Kind: tomlsubset.Bool, Default: flag(false), Layers: inAll, Field: "issue-prompt"},
}

// The open families: a rule id and a runner name are the keys of their tables.
const (
	rulesPrefix      = "rules."
	foregroundPrefix = "budgets.foreground_s."
)

// anyInt is a Min that rejects no integer.
const anyInt = -1 << 31

var severities = []string{"block", "warn", "guide", "off"}

func family(name string) (Key, bool) {
	switch {
	case strings.HasPrefix(name, rulesPrefix) && len(name) > len(rulesPrefix):
		return Key{Name: name, Kind: tomlsubset.String, Enum: severities, Default: str(""), Layers: inRepo,
			Table: "rules", Field: strings.TrimPrefix(name, rulesPrefix)}, true
	case strings.HasPrefix(name, foregroundPrefix) && len(name) > len(foregroundPrefix):
		return Key{Name: name, Kind: tomlsubset.Int, Min: 1, Default: num(20), Layers: inRepo,
			Table: "budgets.foreground_s", Field: strings.TrimPrefix(name, foregroundPrefix)}, true
	}
	return Key{}, false
}

// Lookup finds a key by its schema name, an open-family key included.
func Lookup(name string) (Key, bool) {
	for _, k := range schema {
		if k.Name == name {
			return k, true
		}
	}
	return family(name)
}

// Names lists the fixed keys of the schema, in schema order.
func Names() []string {
	out := make([]string, len(schema))
	for i, k := range schema {
		out[i] = k.Name
	}
	return out
}

// lookupField finds the key a file spells as table.field.
func lookupField(table, field string) (Key, bool) {
	for _, k := range schema {
		if k.Table == table && k.Field == field {
			return k, true
		}
	}
	switch table {
	case "rules":
		return family(rulesPrefix + field)
	case "budgets.foreground_s":
		return family(foregroundPrefix + field)
	}
	return Key{}, false
}

// knownTable reports whether some key is spelled under table.
func knownTable(table string) bool {
	if table == "rules" || table == "budgets.foreground_s" {
		return true
	}
	for _, k := range schema {
		if k.Table == table {
			return true
		}
	}
	return false
}

// Validate checks a value read from a file against the key and returns it
// normalised: an enum is lower-cased and trimmed, as the CI mode always was.
func (k Key) Validate(v tomlsubset.Value) (tomlsubset.Value, error) {
	if v.Kind != k.Kind {
		return v, fmt.Errorf("expects a %s, not a %s", k.Kind, v.Kind)
	}
	switch k.Kind {
	case tomlsubset.String:
		if len(k.Enum) > 0 {
			s := strings.ToLower(strings.TrimSpace(v.S))
			if !slices.Contains(k.Enum, s) {
				return v, fmt.Errorf("%q is not one of %s", v.S, strings.Join(k.Enum, ", "))
			}
			v.S = s
		}
		if k.Check != nil {
			if err := k.Check(v.S); err != nil {
				return v, err
			}
		}
	case tomlsubset.Int:
		if v.N < k.Min {
			return v, fmt.Errorf("%d is below the minimum %d", v.N, k.Min)
		}
	}
	return v, nil
}

// Parse reads a value as typed on a command line: true or false, a whole
// number, a comma-separated list, or the text itself.
func (k Key) Parse(raw string) (tomlsubset.Value, error) {
	var v tomlsubset.Value
	switch k.Kind {
	case tomlsubset.Bool:
		switch raw {
		case "true", "false":
			v = flag(raw == "true")
		default:
			return v, fmt.Errorf("%q is not true or false", raw)
		}
	case tomlsubset.Int:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return v, fmt.Errorf("%q is not a whole number", raw)
		}
		v = num(n)
	case tomlsubset.List:
		v = list()
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				v.List = append(v.List, p)
			}
		}
	default:
		v = str(raw)
	}
	return k.Validate(v)
}

// Display is a value as `config show` prints it.
func Display(v tomlsubset.Value) string {
	switch v.Kind {
	case tomlsubset.Bool:
		return strconv.FormatBool(v.B)
	case tomlsubset.Int:
		return strconv.Itoa(v.N)
	case tomlsubset.List:
		return TOML(v)
	}
	if v.S == "" {
		return `""`
	}
	return v.S
}

// TOML is a value as a file spells it.
func TOML(v tomlsubset.Value) string {
	switch v.Kind {
	case tomlsubset.Bool:
		return strconv.FormatBool(v.B)
	case tomlsubset.Int:
		return strconv.Itoa(v.N)
	case tomlsubset.List:
		parts := make([]string, len(v.List))
		for i, s := range v.List {
			parts[i] = quote(s)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return quote(v.S)
}

func quote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`).Replace(s)
	return `"` + s + `"`
}

// AllowedIn reports whether a file of the layer may declare the key.
func (k Key) AllowedIn(l Layer) bool { return k.Layers.has(l) }
