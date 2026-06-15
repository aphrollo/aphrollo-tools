package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	dir := filepath.Dir(file)
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
			if _, ok := pkg.DevDependencies["vitest"]; ok {
				return Runner{Cmd: "npx", Args: []string{"vitest", "run"}}
			}
			if _, ok := pkg.Dependencies["vitest"]; ok {
				return Runner{Cmd: "npx", Args: []string{"vitest", "run"}}
			}
			if _, ok := pkg.DevDependencies["jest"]; ok {
				return Runner{Cmd: "npx", Args: []string{"jest"}}
			}
		}
	}
	return Runner{Cmd: "npm", Args: []string{"test", "--silent"}}
}

// NarrowToRelatedTests scopes a broad runner to the edited file so PostToolUse
// stays fast (the operator's pyramid: related tests after each edit, the full
// suite at commit). It narrows ONLY for test-file edits, where the relevant
// test is unambiguous; a source edit keeps the broad command, because guessing
// its test file wrong would silently skip the very test that should fail.
func NarrowToRelatedTests(r Runner, target, root string) Runner {
	if ClassifyFile(target) != Test {
		return r
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return r
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
