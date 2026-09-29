package suite

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A Python project whose code lives in a subdirectory of the repo (backend/
// beside a frontend, say) rarely carries pyproject.toml: it has a
// requirements file naming pytest, a setup.cfg or a conftest.py. The walk up
// from a staged .py file used to pass straight over that directory to the
// repo's `.git`, whose root has no runner, so the commit gate said "no
// detected runner" and never proved a staged test red. These are the extra
// signals a pytest root is recognised by, read by FindProjectRoot for a .py
// file and by DetectRunner for the directory it lands on.

// pytestRequirementRe matches a requirements line naming pytest or one of its
// plugins as a package, not a comment or another package that mentions it.
var pytestRequirementRe = regexp.MustCompile(`(?mi)^\s*pytest\b`)

// pytestDeclared reports whether dir declares pytest: a setup.cfg naming it,
// or a requirements*.txt file that lists it.
func pytestDeclared(dir string) bool {
	if data, err := os.ReadFile(filepath.Join(dir, "setup.cfg")); err == nil && strings.Contains(string(data), "pytest") {
		return true
	}
	files, _ := filepath.Glob(filepath.Join(dir, "requirements*.txt"))
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil && pytestRequirementRe.Match(data) {
			return true
		}
	}
	return false
}

// hasConftest reports whether dir holds a conftest.py.
func hasConftest(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "conftest.py"))
	return err == nil
}

// pytestSignalled reports whether dir is a pytest root by the signals
// DetectRunner reads beyond the build files it already knows.
func pytestSignalled(dir string) bool {
	return pytestDeclared(dir) || hasConftest(dir)
}

// hasRootMarker reports whether dir holds any of the ordinary root markers.
func hasRootMarker(dir string) bool {
	for _, m := range rootMarkers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// pythonRootFrom is findRootFrom for a directory of Python code. A directory
// with an ordinary marker is the root exactly as before, and so is one that
// declares pytest. A conftest.py alone marks nothing on its way up (tests/
// carries one and is not where pytest is run from) but is the answer when
// the walk reaches the repo's `.git` with no better marker: the topmost
// directory holding one.
func pythonRootFrom(dir string) string {
	conftest := ""
	for {
		if pytestDeclared(dir) {
			return dir
		}
		if hasConftest(dir) {
			conftest = dir
		}
		if hasRootMarker(dir) {
			if isRepoTop(dir) && conftest != "" {
				return conftest
			}
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return conftest
		}
		dir = parent
	}
}

// isRepoTop reports whether dir is marked by `.git` alone, so that nothing
// but the repository itself makes it a root.
func isRepoTop(dir string) bool {
	for _, m := range rootMarkers {
		if m == ".git" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return false
		}
	}
	return true
}
