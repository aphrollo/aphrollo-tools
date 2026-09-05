package ratchet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// KindGoDepGraphForbids is `dep-graph-forbids`' Go twin: no root package may
// REACH a forbidden one (glob) through the resolved Go import graph — package
// layering as a law, the way the cargo graph already is. Same schema
// (`roots`, `forbidden`, `min_reachable`) and the same reachability walk
// (reachablePaths); Go has no dev/build edge distinction to parametrize
// (`go list`'s default walk already excludes test-only imports), so there is
// no `edges` field here.
const KindGoDepGraphForbids MatcherKind = "go-dep-graph-forbids"

// goListFixtureFile lets a fixture stand in for a real Go module: the law's
// rule is about the RESOLVED import graph, and a checked-in `go list -deps
// -json ./...` document is that graph without needing `go list` to run over
// a synthetic tree.
const goListFixtureFile = "go-list.json"

// goListPackage is the subset of `go list -json`'s per-package object this
// matcher reads: its own import path, its DIRECT imports (Imports, not the
// `-deps`-flattened closure — the walk builds the transitive path itself,
// exactly like the cargo kind does over cargo's direct edges), and whether
// it is a standard-library package (never a root candidate under `roots =
// "*"`).
type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
	Standard   bool     `json:"Standard"`
}

// loadGoListPackages reads the resolved Go package graph: a checked-in
// go-list.json when the tree carries one (fixtures), else `go list -deps
// -json ./...` over the tree itself. `go list -json`'s own output is a
// STREAM of concatenated JSON objects, not an array, so it is decoded one
// value at a time — the same shape a checked-in copy of the real command's
// output would have, unedited.
func loadGoListPackages(root string) ([]goListPackage, error) {
	data, err := os.ReadFile(filepath.Join(root, goListFixtureFile))
	if err != nil {
		cmd := exec.Command("go", "list", "-deps", "-json", "./...")
		cmd.Dir = root
		out, runErr := cmd.Output()
		if runErr != nil {
			return nil, fmt.Errorf("go list -deps -json ./... in %s: %w", root, runErr)
		}
		data = out
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var pkgs []goListPackage
	for dec.More() {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("go list output is not valid JSON: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// goModulePrefix reads go.mod's `module` line, so `roots = "*"` can name
// every package THIS module owns rather than every standard-library or
// third-party package the walk happened to pull in.
func goModulePrefix(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			if mod := strings.TrimSpace(rest); mod != "" {
				return mod, nil
			}
		}
	}
	return "", fmt.Errorf("go.mod has no module line")
}

// goWorkspaceRootNames is every package `roots = "*"` stands for: every
// non-standard package under the module's own import-path prefix — a
// fixture's go-list.json IS its module, exactly like a fixture's
// cargo-metadata.json is its workspace.
func goWorkspaceRootNames(root, prefix string, pkgs []goListPackage) []string {
	var out []string
	for _, p := range pkgs {
		if p.Standard {
			continue
		}
		if p.ImportPath == prefix || strings.HasPrefix(p.ImportPath, prefix+"/") {
			out = append(out, p.ImportPath)
		}
	}
	sort.Strings(out)
	return out
}

// goDepGraphHits walks the resolved Go import graph from each root and
// reports every forbidden package it can reach, keyed by the PATH that
// reaches it — the same reachability walk and the same vacuity refusals
// (a walk that resolved nothing, a wildcard that reached nothing) as the
// cargo kind, over `go list`'s graph instead of `cargo metadata`'s.
func goDepGraphHits(root string, law Law) ([]Hit, error) {
	pkgs, err := loadGoListPackages(root)
	if err != nil {
		return nil, fmt.Errorf("law %q: %w", law.Name, err)
	}
	known := map[string]bool{}
	nameOf := map[string]string{}
	for _, p := range pkgs {
		known[p.ImportPath] = true
		nameOf[p.ImportPath] = p.ImportPath
	}
	deps := map[string][]string{}
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			if known[imp] {
				deps[p.ImportPath] = append(deps[p.ImportPath], imp)
			}
		}
	}

	roots, wildcard := law.Matcher.Roots, false
	if len(roots) == 1 && roots[0] == AllRoots {
		prefix, perr := goModulePrefix(root)
		if perr != nil {
			return nil, fmt.Errorf("law %q: %w", law.Name, perr)
		}
		roots, wildcard = goWorkspaceRootNames(root, prefix, pkgs), true
	}

	var hits []Hit
	reached := 0
	for _, rootName := range roots {
		if !known[rootName] {
			return nil, fmt.Errorf("law %q: no package named %q in the resolved graph", law.Name, rootName)
		}
		paths := reachablePaths(deps, nameOf, rootName, rootName)
		reached += len(paths)
		if len(paths) == 0 && !wildcard {
			return nil, fmt.Errorf(
				"law %q: %q reaches no dependency at all — the walk is broken, and every clean verdict under it is vacuous",
				law.Name, rootName)
		}
		for _, name := range sortedKeys(paths) {
			if !matchesAny(law.Matcher.Forbidden, name) {
				continue
			}
			hits = append(hits, Hit{
				Law: law.Name, File: rootName, Weight: 1,
				Key:  paths[name],
				What: fmt.Sprintf("%s reaches %s (%s)", rootName, name, paths[name]),
			})
		}
	}
	if reached < law.Matcher.MinReachable {
		return nil, fmt.Errorf(
			"law %q: the walk reached %d %s, min_reachable = %d — a verdict over that little data is vacuous",
			law.Name, reached, plural(reached, "package"), law.Matcher.MinReachable)
	}
	if wildcard && reached == 0 {
		return nil, fmt.Errorf(
			"law %q: no package in the module reaches anything — the walk is broken, and every clean verdict under it is vacuous",
			law.Name)
	}
	return hits, nil
}
