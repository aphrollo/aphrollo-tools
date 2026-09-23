package core

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// goEmbedsFile reports whether a `//go:embed` directive in a .go file of
// p's directory, or of an ancestor directory, names p. Go resolves embed
// patterns relative to the directive's own package dir, so a file matched by
// one is compiled into the binary and is Source for the gate.
func goEmbedsFile(p string) bool {
	p = filepath.Clean(p)
	dir := filepath.Dir(p)
	for {
		if embedsFrom(dir, p) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// embedsFrom scans the .go files directly in pkgDir for embed patterns that
// match target, a path under pkgDir.
func embedsFrom(pkgDir, target string) bool {
	rel, err := filepath.Rel(pkgDir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		for _, pattern := range embedPatterns(filepath.Join(pkgDir, e.Name())) {
			if ok, _ := path.Match(pattern, rel); ok {
				return true
			}
		}
	}
	return false
}

// embedPatterns returns every pattern from the `//go:embed` directives in
// one Go file, quotes stripped.
func embedPatterns(goFile string) []string {
	f, err := os.Open(goFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "//go:embed ") {
			continue
		}
		for _, field := range strings.Fields(strings.TrimPrefix(line, "//go:embed ")) {
			out = append(out, strings.Trim(field, "\"`"))
		}
	}
	return out
}
