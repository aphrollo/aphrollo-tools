package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"

	"strings"
)

// A mutant's verdict is only as stable as the code that KILLS it, and that
// code need not live in the mutant's own file or even its own crate: a mutant
// in a.rs may be caught solely through b.rs's behaviour. So the invalidation
// fence follows the workspace's dependency graph, and this file is where that
// graph comes from — the build tool's own answer, never a guess:
//
//	Cargo — `cargo metadata --no-deps`, keeping ONLY dependencies that carry a
//	        path. A registry crate cannot change under a lane; a path
//	        dependency is the workspace's own source.
//	Go    — `go list -deps`, keeping only packages inside this module.
//
// Both answers are keyed the way TreeState keys packages: by the package
// directory relative to the repo root. A tool that is not installed, or a
// directory that is neither, yields NO edges — which fences every package by
// its own files alone. That is the old behaviour, and it is the safe failure:
// it under-carries rather than over-carries.

// workspaceDepsFn is the graph probe, a seam so a plan can be tested without
// a toolchain.
var workspaceDepsFn = workspaceDeps

// workspaceDeps reads the dependency graph of whatever kind of workspace root
// is, empty when it cannot be read.
func workspaceDeps(root string) map[string][]string {
	if root == "" {
		return nil
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		if deps, err := cargoWorkspaceDeps(root); err == nil {
			return deps
		}
	}
	if fileExists(filepath.Join(root, "go.mod")) {
		if deps, err := goWorkspaceDeps(root); err == nil {
			return deps
		}
	}
	return nil
}

// cargoWorkspaceDeps asks cargo. `--no-deps` keeps it to the workspace's own
// manifests, which is both the fast answer and the only one that matters:
// nothing outside the workspace changes under a lane.
func cargoWorkspaceDeps(root string) (map[string][]string, error) {
	cargo := os.Getenv("CARGO")
	if cargo == "" {
		cargo = "cargo"
	}
	out, err := exec.Command(cargo, "metadata", "--no-deps", "--format-version", "1",
		"--manifest-path", filepath.Join(root, "Cargo.toml")).Output()
	if err != nil {
		return nil, err
	}
	return parseCargoWorkspaceDeps(out, root)
}

// parseCargoWorkspaceDeps turns `cargo metadata --no-deps` into package-dir
// edges. Only dependencies with a `path` are edges.
func parseCargoWorkspaceDeps(data []byte, root string) (map[string][]string, error) {
	var meta struct {
		Packages []struct {
			Name         string `json:"name"`
			ManifestPath string `json:"manifest_path"`
			Dependencies []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	deps := map[string][]string{}
	for _, p := range meta.Packages {
		from := relPackageDir(root, filepath.Dir(p.ManifestPath))
		if from == "" && p.ManifestPath == "" {
			continue
		}
		var to []string
		for _, d := range p.Dependencies {
			if d.Path == "" {
				continue
			}
			to = append(to, relPackageDir(root, d.Path))
		}
		if len(to) > 0 {
			deps[from] = dedupeSorted(to)
		}
	}
	return deps, nil
}

// goWorkspaceDeps asks go. Only packages inside this module are edges: a
// module dependency is pinned by go.mod and cannot change under a lane.
func goWorkspaceDeps(root string) (map[string][]string, error) {
	// One package per line: its directory, then every package it depends on.
	// The transitive closure is folded by the fence itself, so the direct
	// edges are all this has to report.
	edges, err := goList(root, "{{.Dir}} {{range .Deps}}{{.}} {{end}}")
	if err != nil {
		return nil, err
	}
	dirOf, err := goPackageDirs(root)
	if err != nil {
		return nil, err
	}
	deps := map[string][]string{}
	for line := range strings.SplitSeq(string(edges), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		from := relPackageDir(root, fields[0])
		var to []string
		for _, imp := range fields[1:] {
			if dir, ok := dirOf[imp]; ok && dir != from {
				to = append(to, dir)
			}
		}
		if len(to) > 0 {
			deps[from] = dedupeSorted(to)
		}
	}
	return deps, nil
}

// goPackageDirs maps every import path in this module to its directory,
// relative to the repo root.
func goPackageDirs(root string) (map[string]string, error) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Dir}}", "./...").Output()
	if err != nil {
		return nil, err
	}
	dirs := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) != 2 {
			continue
		}
		dirs[f[0]] = relPackageDir(root, f[1])
	}
	return dirs, nil
}

// goList runs `go list` over this module with one format, in the module's own
// directory — the answer is about root, not about wherever the gate ran from.
func goList(root, format string) (string, error) {
	cmd := exec.Command("go", "list", "-f", format, "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	return string(out), err
}

// relPackageDir is a package directory as TreeState keys it: forward slashes,
// relative to the repo root, "" for the root's own package.
func relPackageDir(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return ""
	}
	return rel
}
