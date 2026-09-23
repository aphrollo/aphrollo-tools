package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// LevelPrep marks a package a prep PR creates by hand; the generator maps
// files to it but never moves them.
const LevelPrep = -1

// Package is one target package of the split.
type Package struct {
	Name  string
	Level int
	Dir   string // repo-relative, slash-separated
}

// Relocation is one symbol a prep PR moves between files of the unsplit
// package, so that every file maps to exactly one target.
type Relocation struct {
	Symbol string
	From   string
	To     string
}

// Manifest maps every file of the unsplit package to its target package.
// File keys are relative to Root; a key ending in "/" maps every file under
// that directory. Locals are files a carved package owns on its own, such
// as its TestMain: paths relative to Root, never moved and never part of the
// reassembled package.
type Manifest struct {
	Root        string
	Packages    map[string]Package
	Files       map[string]string
	Relocations []Relocation
	Locals      map[string]bool
}

// ParseManifest reads the manifest text format: a `root <dir>` directive,
// then `[packages]` (name level dir), `[files]` (key package), `[relocate]`
// (symbol from-file to-file) and `[local]` (path) sections. `#` starts a
// comment.
func ParseManifest(r io.Reader) (*Manifest, error) {
	m := &Manifest{Packages: map[string]Package{}, Files: map[string]string{}, Locals: map[string]bool{}}
	section := ""
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) == 1 && strings.HasPrefix(fields[0], "[") && strings.HasSuffix(fields[0], "]") {
			section = strings.Trim(fields[0], "[]")
			if section != "packages" && section != "files" && section != "relocate" && section != "local" {
				return nil, fmt.Errorf("manifest:%d: unknown section [%s]", lineNo, section)
			}
			continue
		}
		bad := func(want string) error {
			return fmt.Errorf("manifest:%d: want %s, got %q", lineNo, want, sc.Text())
		}
		switch section {
		case "":
			if len(fields) != 2 || fields[0] != "root" {
				return nil, bad("`root <dir>`")
			}
			m.Root = fields[1]
		case "packages":
			if len(fields) != 3 {
				return nil, bad("`<name> <L<n>|prep> <dir>`")
			}
			level, err := parseLevel(fields[1])
			if err != nil {
				return nil, fmt.Errorf("manifest:%d: %w", lineNo, err)
			}
			name, dir := fields[0], fields[2]
			if path.Base(dir) != name {
				return nil, fmt.Errorf("manifest:%d: package %s lives in %s; the directory name must equal the package name", lineNo, name, dir)
			}
			if _, dup := m.Packages[name]; dup {
				return nil, fmt.Errorf("manifest:%d: package %s declared twice", lineNo, name)
			}
			m.Packages[name] = Package{Name: name, Level: level, Dir: dir}
		case "files":
			if len(fields) != 2 {
				return nil, bad("`<file> <package>`")
			}
			if _, dup := m.Files[fields[0]]; dup {
				return nil, fmt.Errorf("manifest:%d: %s mapped twice", lineNo, fields[0])
			}
			m.Files[fields[0]] = fields[1]
		case "relocate":
			if len(fields) != 3 {
				return nil, bad("`<symbol> <from-file> <to-file>`")
			}
			m.Relocations = append(m.Relocations, Relocation{Symbol: fields[0], From: fields[1], To: fields[2]})
		case "local":
			if len(fields) != 1 {
				return nil, bad("`<path relative to root>`")
			}
			m.Locals[fields[0]] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if m.Root == "" {
		return nil, fmt.Errorf("manifest: no `root <dir>` directive")
	}
	for key, pkg := range m.Files {
		if _, ok := m.Packages[pkg]; !ok {
			return nil, fmt.Errorf("manifest: %s maps to undeclared package %s", key, pkg)
		}
	}
	return m, nil
}

// PackageOf returns the package a file key maps to: its exact entry, else the
// longest directory entry holding it.
func (m *Manifest) PackageOf(key string) (string, bool) {
	if pkg, ok := m.Files[key]; ok {
		return pkg, true
	}
	best, bestPkg := "", ""
	for k, pkg := range m.Files {
		if strings.HasSuffix(k, "/") && strings.HasPrefix(key, k) && len(k) > len(best) {
			best, bestPkg = k, pkg
		}
	}
	return bestPkg, best != ""
}

// Unmapped returns, sorted, every key the manifest does not map.
func (m *Manifest) Unmapped(keys []string) []string {
	var out []string
	for _, k := range keys {
		if _, ok := m.PackageOf(k); !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ParseLevels reads a comma-separated level list such as "L0,L1".
func ParseLevels(s string) (map[int]bool, error) {
	out := map[int]bool{}
	for _, f := range strings.Split(s, ",") {
		level, err := parseLevel(strings.TrimSpace(f))
		if err != nil {
			return nil, err
		}
		if level == LevelPrep {
			return nil, fmt.Errorf("level %q: prep packages are created by hand, never by the generator", f)
		}
		out[level] = true
	}
	return out, nil
}

func parseLevel(s string) (int, error) {
	if s == "prep" {
		return LevelPrep, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "L"))
	if !strings.HasPrefix(s, "L") || err != nil || n < 0 {
		return 0, fmt.Errorf("level %q: want L<n> or prep", s)
	}
	return n, nil
}
