package suite

import (
	"os/exec"
	"path/filepath"

	"strings"
)

// Which packages a change can reach, from the build tool's own answer rather
// than a guess: `go list -deps`, keeping only packages inside this module,
// because a module dependency is pinned by go.mod and cannot change under a
// lane. The answers are keyed by the package directory relative to the repo
// root. A tool that is not installed yields NO edges, which is the safe
// failure: it under-reaches rather than over-reaches.
//
// The Cargo half went with the mutation outcome store's invalidation fence:
// it existed to decide whether a stored verdict still held, and a lane is
// measured on its own tree now.

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
