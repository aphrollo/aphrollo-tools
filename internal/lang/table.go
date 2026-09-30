package lang

import (
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Dir is where a repository keeps its own rows, relative to its root.
const Dir = ".ratchet/languages"

// Two rows carry no extension: they are the lexers a file with no row of its
// own is read by, asked for by name. Neutral reads `//` and `/* */` comments
// and the quote and backtick strings; NeutralHash also reads `#` as a comment.
const (
	Neutral     = "default"
	NeutralHash = "default-hash"
)

//go:embed languages/*.toml
var embedded embed.FS

// Table is the set of rows a tool reads: the embedded defaults, with a
// repository's rows added or replacing the default of the same name.
type Table struct {
	rows   []Language
	byExt  map[string]int
	byFile map[string]int
	byName map[string]int
}

var (
	defaultsOnce sync.Once
	defaults     *Table
	defaultsErr  error
)

// Defaults is the embedded table. The rows ship with the binary and a test
// parses each, so an error here is a build defect and is reported by every
// caller rather than hidden.
func Defaults() (*Table, error) {
	defaultsOnce.Do(func() {
		entries, err := embedded.ReadDir("languages")
		if err != nil {
			defaultsErr = err
			return
		}
		var rows []Language
		for _, e := range entries {
			data, err := embedded.ReadFile("languages/" + e.Name())
			if err != nil {
				defaultsErr = err
				return
			}
			row, err := Parse(string(data), "languages/"+e.Name())
			if err != nil {
				defaultsErr = err
				return
			}
			if want := strings.TrimSuffix(e.Name(), ".toml"); row.Name != want {
				defaultsErr = fmt.Errorf("languages/%s: name %q must equal the file's stem %q", e.Name(), row.Name, want)
				return
			}
			rows = append(rows, row)
		}
		defaults, defaultsErr = build(rows)
	})
	return defaults, defaultsErr
}

// build indexes rows, refusing two rows that claim one extension or file name.
func build(rows []Language) (*Table, error) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	t := &Table{rows: rows, byExt: map[string]int{}, byFile: map[string]int{}, byName: map[string]int{}}
	for i, r := range rows {
		if _, dup := t.byName[r.Name]; dup {
			return nil, fmt.Errorf("language %q is defined twice", r.Name)
		}
		t.byName[r.Name] = i
		for _, ext := range r.Extensions {
			if j, dup := t.byExt[ext]; dup {
				return nil, fmt.Errorf("extension %s is claimed by both %q and %q", ext, rows[j].Name, r.Name)
			}
			t.byExt[ext] = i
		}
		for _, name := range r.Filenames {
			if j, dup := t.byFile[name]; dup {
				return nil, fmt.Errorf("file name %s is claimed by both %q and %q", name, rows[j].Name, r.Name)
			}
			t.byFile[name] = i
		}
	}
	return t, nil
}

// Extend returns a table with extra rows laid over t: a row replaces the row
// of its name, and takes an extension or file name from whichever row held it.
func (t *Table) Extend(extra []Language) (*Table, error) {
	own := map[string]bool{}
	for _, r := range extra {
		own[r.Name] = true
	}
	var rows []Language
	for _, r := range t.rows {
		if own[r.Name] {
			continue
		}
		rows = append(rows, withoutClaims(r, extra))
	}
	return build(append(rows, extra...))
}

// withoutClaims drops from r the extensions and file names a later row claims.
func withoutClaims(r Language, extra []Language) Language {
	taken, takenFile := map[string]bool{}, map[string]bool{}
	for _, e := range extra {
		for _, ext := range e.Extensions {
			taken[ext] = true
		}
		for _, name := range e.Filenames {
			takenFile[name] = true
		}
	}
	r.Extensions = keepUnless(r.Extensions, taken)
	r.Filenames = keepUnless(r.Filenames, takenFile)
	return r
}

func keepUnless(list []string, drop map[string]bool) []string {
	var out []string
	for _, s := range list {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

// ForRoot is the table of the repository at root: the defaults plus its
// `.ratchet/languages/*.toml`. A repository with no such directory reads the
// defaults.
func ForRoot(root string) (*Table, error) {
	base, err := Defaults()
	if err != nil {
		return nil, err
	}
	if root == "" {
		return base, nil
	}
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return base, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var extra []Language
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, e.Name()), err)
		}
		row, err := Parse(string(data), Dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		if want := strings.TrimSuffix(e.Name(), ".toml"); row.Name != want {
			return nil, fmt.Errorf("%s/%s: name %q must equal the file's stem %q", Dir, e.Name(), row.Name, want)
		}
		extra = append(extra, row)
	}
	if len(extra) == 0 {
		return base, nil
	}
	return base.Extend(extra)
}

// Rows lists every row, ordered by name.
func (t *Table) Rows() []Language { return t.rows }

// Named finds a row by name.
func (t *Table) Named(name string) (Language, bool) {
	i, ok := t.byName[name]
	if !ok {
		return Language{}, false
	}
	return t.rows[i], true
}

// For finds the row that owns a path: by its exact base name first, then by
// its lowercased extension.
func (t *Table) For(file string) (Language, bool) {
	base := path.Base(filepath.ToSlash(file))
	if i, ok := t.byFile[base]; ok {
		return t.rows[i], true
	}
	if i, ok := t.byExt[strings.ToLower(filepath.Ext(base))]; ok {
		return t.rows[i], true
	}
	return Language{}, false
}
