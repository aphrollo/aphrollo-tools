package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// Options says what to read: the repo's root (its trellis.toml, and its
// aphrollo.toml as an alias), the repo's id in the user's [repo."<id>"]
// sections, the config root (ConfigRoot when empty), and the per-command flags
// that win over every layer, as schema key to typed text.
type Options struct {
	Repo   string
	RepoID string
	Root   string
	Flags  map[string]string
}

// Setting is one key's effective value and where it came from. File and Line
// name the declaration; Alias names the aphrollo.toml key(s) it was read
// through; Overrides names the aliases a trellis.toml declaration shadowed.
// Fallback marks a built-in value read because the layer that declared the key
// declared it wrongly.
type Setting struct {
	Key       string
	Value     tomlsubset.Value
	Layer     Layer
	File      string
	Line      int
	Alias     string
	Overrides []string
	Fallback  bool
}

// Diagnostic is one thing a file or a flag got wrong.
type Diagnostic struct {
	File  string
	Layer Layer
	Line  int
	Key   string
	Msg   string
}

func (d Diagnostic) String() string {
	where := d.File
	if where == "" {
		where = "--" + d.Key
	}
	if d.Line > 0 {
		where += ":" + strconv.Itoa(d.Line)
	}
	key := d.Key
	if key != "" && d.File != "" {
		key += ": "
	} else {
		key = ""
	}
	return fmt.Sprintf("%s: %s layer: %s%s", where, d.Layer, key, d.Msg)
}

// Config is every key resolved.
type Config struct {
	settings map[string]Setting
	names    []string
	diags    []Diagnostic
	legacy   []Legacy
}

// Get is the key's setting: an open-family key nobody declared reads its
// family's default; a name outside the schema is the zero Setting.
func (c *Config) Get(key string) Setting {
	if s, ok := c.settings[key]; ok {
		return s
	}
	if k, ok := family(key); ok {
		return Setting{Key: key, Value: k.Default, Layer: BuiltIn}
	}
	return Setting{}
}

// Settings lists every fixed key, then each open-family key a layer declared,
// in a stable order.
func (c *Config) Settings() []Setting {
	out := make([]Setting, len(c.names))
	for i, n := range c.names {
		out[i] = c.settings[n]
	}
	return out
}

// Diagnostics lists every misread, in the order the layers were read.
func (c *Config) Diagnostics() []Diagnostic { return c.diags }

// claim is one layer's declaration of one key.
type claim struct {
	key      Key
	layer    Layer
	rank     int
	file     string
	line     int
	value    tomlsubset.Value
	invalid  bool
	alias    string
	reported bool
}

// The order claims win in: the higher rank beats the lower.
const (
	rankUser = iota + 1
	rankUserRepo
	rankAlias
	rankRepo
	rankFlag
)

// Load reads every layer. It never fails: what it cannot read is named in
// Diagnostics, and the built-in value stands in its place.
func Load(o Options) *Config {
	r := &reader{}
	root := o.Root
	if root == "" {
		root = ConfigRoot()
	}
	if root != "" {
		r.userFile(filepath.Join(root, "config.toml"), o.RepoID)
	}
	if o.Repo != "" {
		r.repoFiles(o.Repo)
	}
	r.flags(o.Flags)
	return r.resolve()
}

type reader struct {
	claims []claim
	diags  []Diagnostic
	legacy []Legacy
}

func (r *reader) diag(file string, layer Layer, line int, key, msg string) {
	r.diags = append(r.diags, Diagnostic{File: file, Layer: layer, Line: line, Key: key, Msg: msg})
}

// userFile reads the user's config.toml: its root keys, then the keys of the
// [repo."<id>"] section that names this repo.
func (r *reader) userFile(path, repoID string) {
	doc := r.parse(path, User, normalizeUser, nil)
	if doc == nil {
		return
	}
	r.claimsFrom(doc, path, User, rankUser, "", func(name string) bool {
		return name == "repo" || strings.HasPrefix(name, "repo.")
	})
	if repoID != "" {
		section := "repo." + repoID
		r.claimsFrom(doc, path, User, rankUserRepo, section, nil)
	}
}

// repoFiles reads the repo's trellis.toml, and aphrollo.toml as its alias.
func (r *reader) repoFiles(repo string) {
	r.aliasFile(filepath.Join(repo, "aphrollo.toml"))
	path := filepath.Join(repo, "trellis.toml")
	if doc := r.parse(path, Repo, nil, nil); doc != nil {
		r.claimsFrom(doc, path, Repo, rankRepo, "", nil)
	}
}

// claimsFrom turns a parsed file's tables into claims. section selects a
// sub-tree: "" is the whole file less the tables skip names, "repo.<id>" is
// that section and its sub-tables, read as if they were the file's own.
func (r *reader) claimsFrom(doc *tomlsubset.Document, path string, layer Layer, rank int, section string, skip func(string) bool) {
	visit := func(t *tomlsubset.Table, sub string) {
		if sub != "" && !knownTable(sub) {
			r.diag(path, layer, t.Line, t.Name, "unknown table")
			return
		}
		for _, field := range t.Seen {
			v := t.Keys[field]
			k, ok := lookupField(sub, field)
			if !ok {
				name := field
				if sub != "" {
					name = sub + "." + field
				}
				r.diag(path, layer, v.Line, name, "unknown key")
				continue
			}
			if !k.Layers.has(layer) {
				r.diag(path, layer, v.Line, k.Name, fmt.Sprintf("the %s layer may not declare this key", layer))
				continue
			}
			c := claim{key: k, layer: layer, rank: rank, file: path, line: v.Line}
			nv, err := k.Validate(v)
			c.value = nv
			if err != nil {
				c.invalid = true
				r.diag(path, layer, v.Line, k.Name, err.Error()+"; using the built-in value")
				c.reported = true
			}
			r.claims = append(r.claims, c)
		}
	}
	if section == "" {
		visit(doc.Root, "")
	}
	for _, t := range doc.Tables {
		switch {
		case section == "":
			if skip != nil && skip(t.Name) {
				continue
			}
			visit(t, t.Name)
		case t.Name == section:
			visit(t, "")
		case strings.HasPrefix(t.Name, section+"."):
			visit(t, strings.TrimPrefix(t.Name, section+"."))
		}
	}
}

// flags reads the per-command flags.
func (r *reader) flags(flags map[string]string) {
	names := make([]string, 0, len(flags))
	for n := range flags {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		k, ok := Lookup(name)
		if !ok {
			r.diag("", Flag, 0, name, "unknown key")
			continue
		}
		v, err := k.Parse(flags[name])
		c := claim{key: k, layer: Flag, rank: rankFlag, value: v, reported: true}
		if err != nil {
			c.invalid = true
			r.diag("", Flag, 0, name, err.Error()+"; using the next layer's value")
		}
		r.claims = append(r.claims, c)
	}
}

// resolve picks, per key, the claim that wins.
func (r *reader) resolve() *Config {
	cfg := &Config{settings: map[string]Setting{}, diags: r.diags, legacy: r.legacy}
	best := map[string]claim{}
	for _, c := range r.claims {
		if cur, ok := best[c.key.Name]; !ok || c.rank >= cur.rank {
			best[c.key.Name] = c
		}
	}
	for _, k := range schema {
		cfg.names = append(cfg.names, k.Name)
		cfg.settings[k.Name] = settingFor(k, best, r.claims)
	}
	var open []string
	for name := range best {
		if _, fixed := cfg.settings[name]; !fixed {
			open = append(open, name)
		}
	}
	sort.Strings(open)
	for _, name := range open {
		cfg.names = append(cfg.names, name)
		cfg.settings[name] = settingFor(best[name].key, best, r.claims)
	}
	return cfg
}

func settingFor(k Key, best map[string]claim, all []claim) Setting {
	c, ok := best[k.Name]
	if !ok {
		return Setting{Key: k.Name, Value: k.Default, Layer: BuiltIn}
	}
	if c.invalid {
		// The layer's own value is built-in, not the layer beneath it, and
		// never off.
		return Setting{Key: k.Name, Value: k.Default, Layer: BuiltIn, Fallback: true}
	}
	s := Setting{Key: k.Name, Value: c.value, Layer: c.layer, File: c.file, Line: c.line, Alias: c.alias}
	if c.alias == "" {
		for _, o := range all {
			if o.key.Name == k.Name && o.alias != "" && !o.invalid {
				s.Overrides = append(s.Overrides, o.alias)
			}
		}
	}
	return s
}

var lineErr = regexp.MustCompile(`^line (\d+): `)

// parse reads path with tomlsubset, a line at a time as far as it takes: a line
// the reader refuses is named, set aside, and the file is read again without
// it, so one bad line never costs the keys beside it. onBad hears each refused
// line; nil means the refusal is a diagnostic naming the line's key.
func (r *reader) parse(path string, layer Layer, normalize func(string) string, onBad func(lineNo int, line, msg string)) *tomlsubset.Document {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(data)
	if normalize != nil {
		text = normalize(text)
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for range len(lines) + 1 {
		doc, err := tomlsubset.Parse(strings.Join(lines, "\n"))
		if err == nil {
			return doc
		}
		m := lineErr.FindStringSubmatch(err.Error())
		if m == nil {
			r.diag(path, layer, 0, "", err.Error())
			return nil
		}
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > len(lines) {
			r.diag(path, layer, 0, "", err.Error())
			return nil
		}
		msg := strings.TrimPrefix(err.Error(), m[0])
		bad := lines[n-1]
		if onBad != nil {
			onBad(n, bad, msg)
		} else {
			r.badLine(path, layer, n, bad, msg)
		}
		if strings.HasPrefix(strings.TrimSpace(bad), "[") {
			// Its keys must not move into the table above.
			lines[n-1] = "[__refused." + strconv.Itoa(n) + "]"
		} else {
			lines[n-1] = ""
		}
	}
	return nil
}

// badLine names a refused line of a strict file, and, when it was a key the
// schema knows by its root spelling, makes the layer's value for it invalid.
func (r *reader) badLine(path string, layer Layer, n int, line, msg string) {
	field, _, _ := strings.Cut(line, "=")
	field = strings.TrimSpace(field)
	rank := rankRepo
	if layer == User {
		rank = rankUser
	}
	if k, ok := lookupField("", field); ok && k.Layers.has(layer) {
		r.diag(path, layer, n, k.Name, msg+"; using the built-in value")
		r.claims = append(r.claims, claim{key: k, layer: layer, rank: rank, file: path, line: n, invalid: true, reported: true})
		return
	}
	r.diag(path, layer, n, field, msg)
}

var userHeader = regexp.MustCompile(`^(\s*\[\s*repo\.)"([^"]*)"`)
var bareID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// normalizeUser spells a [repo."<id>"] header the way the table reader takes
// it, line for line, so every line number is the file's own.
func normalizeUser(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		m := userHeader.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		id := m[2]
		if !bareID.MatchString(id) {
			id = "__invalid__"
		}
		lines[i] = m[1] + id + l[len(m[0]):]
	}
	return strings.Join(lines, "\n")
}
