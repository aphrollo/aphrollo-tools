package suite

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// nextest does not run doctests — it says so itself and always has — so a
// gate built on it never executes them. A crate can therefore land a
// `compile_fail` proof (the only way to assert that something must NOT
// compile) and have it never run: the test reads as coverage and constrains
// nothing. This stage is the answer, and it is scoped: plain `cargo test
// --doc` for the touched crates that actually carry a doc fence, skipped
// entirely for the ones that do not.

// doctestRunners is one plain-cargo doctest run per touched package that has
// doctests, resolved against the workspace's own member directories.
func doctestRunners(ws string, pkgs []string) []Runner {
	dirs := packageDirs(ws)
	var out []Runner
	for _, pkg := range pkgs {
		dir, ok := dirs[pkg]
		if !ok || !packageHasDoctests(dir) {
			continue
		}
		out = append(out, Runner{Cmd: "cargo", Args: []string{"test", "-p", pkg, "--doc"}, Dir: ws})
	}
	return out
}

// packageDirs maps every package name under ws to its own directory. The walk
// skips target dirs and dot dirs, which is where a vendored manifest hides.
func packageDirs(ws string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(ws, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != ws && (name == "target" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "Cargo.toml" {
			return nil
		}
		if name := cargoPackageName(path); name != "" {
			out[name] = filepath.Dir(path)
		}
		return nil
	})
	return out
}

// packageHasDoctests reports whether any file under the crate's src/ opens a
// code fence inside a doc comment. A fence anywhere else (a string, an
// ordinary comment) is not a doctest and must not buy a whole cargo run.
func packageHasDoctests(dir string) bool {
	found := false
	_ = filepath.WalkDir(filepath.Join(dir, "src"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".rs") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for line := range strings.Lines(string(data)) {
			t := strings.TrimSpace(line)
			if !strings.HasPrefix(t, "///") && !strings.HasPrefix(t, "//!") {
				continue
			}
			if strings.Contains(t, "```") {
				found = true
				return fs.SkipAll
			}
		}
		return nil
	})
	return found
}
