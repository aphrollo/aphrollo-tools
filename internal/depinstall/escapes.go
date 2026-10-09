package depinstall

import (
	"os"
	"path/filepath"
	"strings"
)

// Escape is a package of an install whose real path lies outside the boundary.
type Escape struct {
	Name string // package name as it sits in node_modules ("@scope/pkg" for a scoped one)
	Real string // where it really resolves
}

// Escapes lists the top-level packages of the install at nodeModules that
// resolve outside boundary. Node and vite dedupe a module by its real path, so
// one install whose react lives in another lane's tree loads two Reacts beside
// the rest of it. An install is self-contained when the list is empty. Only
// links are resolved: a real directory under a node_modules that is itself
// inside the boundary is inside it, which keeps the check to one readdir and
// one resolve per link. When node_modules itself resolves outside, it is the
// one escape. An unreadable or unresolvable entry counts as an escape: unproven
// means not contained.
func Escapes(nodeModules, boundary string) []Escape {
	realBoundary, err := realPath(boundary)
	if err != nil {
		return []Escape{{Name: NodeModules, Real: ""}}
	}
	realNM, err := realPath(nodeModules)
	if err != nil || !within(realBoundary, realNM) {
		return []Escape{{Name: NodeModules, Real: realNM}}
	}
	var out []Escape
	entries, err := os.ReadDir(realNM)
	if err != nil {
		return []Escape{{Name: NodeModules, Real: realNM}}
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // .bin, .pnpm, .cache: tooling, not a package
		}
		if strings.HasPrefix(name, "@") && !isLink(entryType(e)) {
			scoped, err := os.ReadDir(filepath.Join(realNM, name))
			if err != nil {
				out = append(out, Escape{Name: name})
				continue
			}
			for _, s := range scoped {
				out = appendIfEscapes(out, realBoundary, filepath.Join(realNM, name, s.Name()), name+"/"+s.Name(), s)
			}
			continue
		}
		out = appendIfEscapes(out, realBoundary, filepath.Join(realNM, name), name, e)
	}
	return out
}

func appendIfEscapes(out []Escape, realBoundary, path, name string, e os.DirEntry) []Escape {
	if !isLink(entryType(e)) {
		return out
	}
	real, err := realPath(path)
	if err != nil || !within(realBoundary, real) {
		return append(out, Escape{Name: name, Real: real})
	}
	return out
}

// within reports whether path is dir or lies under it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RealPath is where path really is: symlinks and, on Windows, directory
// junctions followed, which filepath.EvalSymlinks does not do there.
func RealPath(path string) (string, error) { return realPath(path) }
