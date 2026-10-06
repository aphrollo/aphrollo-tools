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
	project := relOrDot(repo, root)
	return Unit{ID: languageOf(file) + ":" + project, Project: project, Kind: unitProjectRoot}, true
}

// languageOf is the name of the language row that owns file, "other" for a file no
// row owns. A JavaScript file is named for the TypeScript row: the two are one
// node project, whose tests (vitest, jest) read .ts and .js files alike, so a unit
// per spelling would split one project's red from its code edits.
func languageOf(file string) string {
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
