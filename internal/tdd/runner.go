package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Runner is a test command: a program and its arguments, run from the project
// root. Keeping it a plain value makes detection and narrowing pure and
// testable; only PostToolUse actually executes it.
type Runner struct {
	Cmd  string
	Args []string
}

// rootMarkers identify a project root, walking up from an edited file. Order
// does not matter for detection — the FIRST directory containing ANY marker is
// the root — but the marker found also drives runner detection.
var rootMarkers = []string{
	"go.mod", "Cargo.toml", "pyproject.toml", "setup.py", "pytest.ini",
	"package.json", ".git",
}

// FindProjectRoot walks up from a file path to the nearest directory holding a
// project marker, returning "" if none is found (the gates then do nothing).
func FindProjectRoot(file string) string {
	return findRootFrom(filepath.Dir(file))
}

// findRootFrom walks up from a directory (inclusive) to the nearest project
// root. The session hooks start here with a cwd; the edit hooks reach it via
// FindProjectRoot with the edited file's directory.
func findRootFrom(dir string) string {
	for {
		for _, m := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// DetectRunner picks the test command for a project root from its build files.
// It reports false when the project uses a toolchain the gates don't know, so
// PostToolUse can stay silent rather than guess.
func DetectRunner(root string) (Runner, bool) {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		return Runner{Cmd: "go", Args: []string{"test", "./..."}}, true
	case has("Cargo.toml"):
		return Runner{Cmd: "cargo", Args: []string{"test"}}, true
	case has("pyproject.toml"), has("setup.py"), has("pytest.ini"):
		return Runner{Cmd: "pytest", Args: []string{"-q"}}, true
	case has("package.json"):
		return jsRunner(root), true
	}
	return Runner{}, false
}

// jsTestRunners is the precedence-ordered table mapping a package.json test
// dependency to its direct runner. The first match wins, so vitest is preferred
// over jest. vitest matches whether it sits in dev or prod dependencies; jest
// matches only as a dev dependency. A package with no match falls back to the
// generic `npm test` script.
var jsTestRunners = []struct {
	dep      string
	prodDeps bool // also match the dependency in prod `dependencies`
	cmd      string
	args     []string
}{
	{dep: "vitest", prodDeps: true, cmd: "npx", args: []string{"vitest", "run"}},
	{dep: "jest", prodDeps: false, cmd: "npx", args: []string{"jest"}},
}

// jsRunner reads package.json to distinguish vitest/jest from a generic npm
// test script, so the run is direct and fast where possible.
func jsRunner(root string) Runner {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err == nil {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			for _, jr := range jsTestRunners {
				_, dev := pkg.DevDependencies[jr.dep]
				_, prod := pkg.Dependencies[jr.dep]
				if dev || (jr.prodDeps && prod) {
					return Runner{Cmd: jr.cmd, Args: jr.args}
				}
			}
		}
	}
	return Runner{Cmd: "npm", Args: []string{"test", "--silent"}}
}

// NarrowToRelatedTests scopes a broad runner to the edited file so PostToolUse
// stays fast (the operator's pyramid: related tests after each edit, the full
// suite at commit, the full suite again in CI).
//
// A TEST-file edit runs that test directly — the relevant test is unambiguous.
// A SOURCE-file edit runs only the tests whose import graph reaches the edited
// file, via each runner's related-tests mode (vitest `related`, jest
// `--findRelatedTests`, go's package granularity). Where the runner has no
// related-tests mode (cargo, pytest, a generic `npm test` script, or an unknown
// command) the broad command is preserved, since the full suite still guards
// the commit and CI. A source file with zero related tests runs nothing and
// exits clean — the existing green/scaffolding outcome, not a failure.
func NarrowToRelatedTests(r Runner, target, root string) Runner {
	kind := ClassifyFile(target)
	if kind != Test && kind != Source {
		return r
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return r
	}
	if kind == Source {
		return narrowSourceEdit(r, rel)
	}
	switch r.Cmd {
	case "go":
		return Runner{Cmd: "go", Args: []string{"test", "./" + filepath.Dir(rel) + "/..."}}
	case "pytest":
		return Runner{Cmd: "pytest", Args: []string{"-q", rel}}
	case "npx":
		return Runner{Cmd: r.Cmd, Args: append(append([]string{}, r.Args...), rel)}
	}
	return r
}

// narrowToStaged scopes a broad runner to the related tests of the UNION of a
// commit's staged source+test files, for the precommit mechanical stage. It is
// the multi-file analog of NarrowToRelatedTests: commit-time is a fast scoped
// check, and CI runs the full suite at submit as the authoritative gate.
//
// It reports (scoped, true) when the runner has a related mode (vitest
// `related`, jest `--findRelatedTests`, go's deduped package dirs); otherwise
// (the original runner, false) so the caller keeps the full-suite fallback. A
// staged file with zero related tests makes the runner exit clean (the existing
// green/WritingTest outcome), not a failure, so scoping never manufactures a
// block. files are repo-root-relative.
//
// Go vs JS scope asymmetry: Go scopes to PACKAGE granularity (`go test ./pkg`),
// so a regression a staged change introduces in another package's IMPORTERS is
// not caught at precommit. vitest/jest scope to the IMPORTER GRAPH (`related` /
// `--findRelatedTests`), so dependents of a staged file ARE covered. This gap is
// acceptable because CI runs the full suite at submit as the authoritative gate.
func narrowToStaged(r Runner, files []string) (Runner, bool) {
	if len(files) == 0 {
		return r, false
	}
	switch r.Cmd {
	case "go":
		// Dedupe the package dir of each staged file; a root-level file maps to
		// the "." package. Sorted for a deterministic command.
		seen := map[string]bool{}
		var pkgs []string
		for _, f := range files {
			dir := filepath.Dir(f)
			pkg := "./" + dir
			if dir == "." {
				pkg = "."
			}
			if !seen[pkg] {
				seen[pkg] = true
				pkgs = append(pkgs, pkg)
			}
		}
		sort.Strings(pkgs)
		return Runner{Cmd: "go", Args: append([]string{"test"}, pkgs...)}, true
	case "npx":
		switch {
		case len(r.Args) > 0 && r.Args[0] == "vitest":
			args := append([]string{"vitest", "related"}, files...)
			return Runner{Cmd: "npx", Args: append(args, "--run")}, true
		case len(r.Args) > 0 && r.Args[0] == "jest":
			args := append([]string{"jest", "--findRelatedTests"}, files...)
			return Runner{Cmd: "npx", Args: args}, true
		}
	}
	return r, false
}

// narrowSourceEdit builds the related-tests command for a source-file edit,
// dispatching on the detected runner. The npx branch keys off the runner name
// (first arg) because vitest and jest expose different related-tests flags.
// Runners without a related mode return unchanged (full-suite fallback).
func narrowSourceEdit(r Runner, rel string) Runner {
	switch r.Cmd {
	case "go":
		return Runner{Cmd: "go", Args: []string{"test", "./" + filepath.Dir(rel)}}
	case "npx":
		switch {
		case len(r.Args) > 0 && r.Args[0] == "vitest":
			return Runner{Cmd: "npx", Args: []string{"vitest", "related", rel, "--run"}}
		case len(r.Args) > 0 && r.Args[0] == "jest":
			return Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", rel}}
		}
	}
	return r
}
