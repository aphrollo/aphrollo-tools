package shadow

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// Unit is the part of a repository the TDD machine keeps one state for.
//
//   - Go: the package, named by its directory relative to the module root. A module
//     that is not the repository's root carries its own path from the repository
//     root in front, so two modules of one repository never share a unit; a module
//     at the repository root is its own root, and its root package is ".".
//   - Every other language: the project root the edit hook resolves for the file
//     (the nearest build manifest: Cargo.toml, package.json, pyproject.toml and so
//     on), relative to the repository root. The language rows of internal/lang
//     carry test-name patterns, which say whether a file is a test and not what
//     unit it is in, so they hold no root to read. The project root is the one
//     unit these languages have, and it is named so (Kind projectRoot): a
//     language with a finer unit would need its own row here. The id leads with the
//     language row's name ("rust:crates/engine"), so a Rust crate and a node
//     package at the same path, or two languages at the repository root ("."), never
//     share a unit.
type Unit struct {
	ID      string // the key in the lane record; a non-Go unit's leads with its language
	Project string // the project root, relative to the repository root ("." for the root)
	Pkg     string // a Go package's directory relative to its module root; "" for a project-root unit
	Kind    string // unitGoPackage or unitProjectRoot
}

// The kinds of unit.
const (
	unitGoPackage   = "go-package"
	unitProjectRoot = "project-root"
)

// UnitOf names the unit of file. projectRoot resolves a non-Go file's project
// root (the edit hook's marker walk); "" from it, or a file no manifest holds, has
// no unit and ok is false. The walk reads directory entries and spawns nothing.
func UnitOf(file string, projectRoot func(string) string) (Unit, bool) {
	file = filepath.Clean(file)
	dir := filepath.Dir(file)
	repo := findUp(dir, ".git")
	if strings.EqualFold(filepath.Ext(file), ".go") {
		if mod := findUp(dir, "go.mod"); mod != "" {
			pkg := relSlash(mod, dir)
			project := relOrDot(repo, mod)
			return Unit{ID: joinUnit(project, pkg), Project: project, Pkg: pkg, Kind: unitGoPackage}, true
		}
	}
	root := projectRoot(file)
	if root == "" {
		return Unit{}, false
	}
	lang := languageOf(file)
	if !hasOwnManifest(lang, root) {
		return Unit{}, false
	}
	project := relOrDot(repo, root)
	return Unit{ID: lang + ":" + project, Project: project, Kind: unitProjectRoot}, true
}

// languageOf is the name of the language row that owns file, "other" for a file no
// row owns. A JavaScript file is named for the TypeScript row: the two are one
// node project, whose tests (vitest, jest) read .ts and .js files alike, so a unit
// per spelling would split one project's red from its code edits.
func languageOf(file string) string {
	if componentUnits[strings.ToLower(filepath.Ext(file))] {
		return "typescript"
	}
	t, err := lang.Defaults()
	if err != nil {
		return "other"
	}
	if l, ok := t.For(file); ok {
		if l.Name == "javascript" {
			return "typescript"
		}
		return l.Name
	}
	return "other"
}

// componentUnits are the single-file components no language row owns: they are
// code of their node package, which its vitest or jest tests mount, so a red of
// those tests must open them (core.ClassifyFile already counts them as source).
var componentUnits = map[string]bool{".svelte": true, ".vue": true}

// findUp is the nearest directory from dir upward holding name, "" when none does.
func findUp(dir, name string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// relSlash is dir relative to base with forward slashes, "." for base itself.
func relSlash(base, dir string) string {
	r, err := filepath.Rel(base, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(r)
}

// relOrDot is dir relative to repo, "." for the repository's own root and for a
// project with no repository around it.
func relOrDot(repo, dir string) string {
	if repo == "" {
		return "."
	}
	return relSlash(repo, dir)
}

// joinUnit puts a module's path in front of a package's.
func joinUnit(project, pkg string) string {
	j := path.Join(project, pkg)
	if j == "" {
		return "."
	}
	return j
}

// LangOfUnit is the language a unit id belongs to, as stats reads agreement per
// language: a Go package's id has no language prefix, a project-root unit's leads
// with its language row's name, and the node rows (typescript, javascript) are
// "ts". "" for no unit.
func LangOfUnit(id string) string {
	if id == "" {
		return ""
	}
	name, _, ok := strings.Cut(id, ":")
	if !ok {
		return "go"
	}
	if name == "typescript" || name == "javascript" {
		return "ts"
	}
	return name
}

// LangOfCommand is the language of a test runner's command word, "" for one that
// names none.
func LangOfCommand(cmd string) string {
	switch strings.ToLower(strings.TrimSuffix(filepath.Base(cmd), filepath.Ext(cmd))) {
	case "go":
		return "go"
	case "python", "python3", "pytest", "py":
		return "python"
	case "npx", "npm", "node", "vitest", "jest", "pnpm", "yarn", "bun":
		return "ts"
	case "cargo":
		return "rust"
	}
	return ""
}

// ownManifests are the files that make a directory a project of a language, by the
// language row's name; a language not listed takes any root the edit hook found.
var ownManifests = map[string][]string{
	"python":     {"pyproject.toml", "setup.py", "setup.cfg", "pytest.ini", "tox.ini", "Pipfile", "conftest.py", "requirements*.txt"},
	"typescript": {"package.json"},
	"rust":       {"Cargo.toml"},
}

// hasOwnManifest reports whether root holds a manifest of lang. The edit hook's
// walk stops at the nearest manifest of any language, so a Python file under a node
// package resolves to the node root: that is no Python project, and has no unit.
func hasOwnManifest(lang, root string) bool {
	names, ok := ownManifests[lang]
	if !ok {
		return true
	}
	for _, n := range names {
		if m, _ := filepath.Glob(filepath.Join(root, n)); len(m) > 0 {
			return true
		}
	}
	return false
}
