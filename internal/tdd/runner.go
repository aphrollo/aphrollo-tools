package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
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
	"package.json", "build.zig", "build.zig.zon", ".git",
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
		// A checked-in nextest config is the project saying "this suite is
		// meant for nextest" (partitioned test groups, per-test timeouts);
		// honour it when the binary is actually installed — 2-3x on
		// link-heavy suites. Either half missing → plain `cargo test`.
		if has(filepath.Join(".config", "nextest.toml")) && nextestInstalled() {
			return Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}, true
		}
		return Runner{Cmd: "cargo", Args: []string{"test"}}, true
	case has("pyproject.toml"), has("setup.py"), has("pytest.ini"):
		return Runner{Cmd: "pytest", Args: []string{"-q"}}, true
	case has("package.json"):
		return jsRunner(root), true
	case has("build.zig"), has("build.zig.zon"):
		// zeta's build.zig defines a `test` step (mod+exe+integration), so
		// `zig build test` is the canonical full-suite runner. It has no
		// related-tests mode, so narrowing leaves it unchanged (see
		// narrowSourceEdit / narrowToStaged — same fallback as cargo/pytest).
		return Runner{Cmd: "zig", Args: []string{"build", "test"}}, true
	}
	return Runner{}, false
}

// nextestInstalled reports whether the cargo-nextest subcommand binary is on
// PATH. Selecting nextest without it would turn every gate run into
// "error: no such command" — a manufactured block.
func nextestInstalled() bool {
	_, err := exec.LookPath("cargo-nextest")
	return err == nil
}

// cargoRunArgs is the verb prefix the detected cargo runner uses — `nextest
// run` or `test` — so narrowing rebuilds scoped commands without forfeiting
// nextest.
func cargoRunArgs(r Runner) []string {
	if len(r.Args) > 0 && r.Args[0] == "nextest" {
		return []string{"nextest", "run"}
	}
	return []string{"test"}
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
	// Every runner below takes the path in its command line; forward slashes
	// work everywhere, Windows backslashes break go package paths and confuse
	// vitest/jest matching.
	rel = filepath.ToSlash(rel)
	if kind == Source {
		return narrowSourceEdit(r, rel, root)
	}
	switch r.Cmd {
	case "go":
		return Runner{Cmd: "go", Args: []string{"test", "./" + path.Dir(rel) + "/..."}}
	case "pytest":
		return Runner{Cmd: "pytest", Args: []string{"-q", rel}}
	case "npx":
		return Runner{Cmd: r.Cmd, Args: append(append([]string{}, r.Args...), rel)}
	case "cargo":
		// A tests/*.rs (or tests/<dir>/*.rs) edit compiles ONE test binary
		// instead of every target in the crate — on a large-dependency crate
		// that is the difference between seconds and a timeout. A Rust test
		// file outside tests/ is a #[cfg(test)] unit module: lib target.
		if name := cargoTestTarget(rel); name != "" {
			return Runner{Cmd: "cargo", Args: append(cargoRunArgs(r), "--test", name)}
		}
		return Runner{Cmd: "cargo", Args: append(cargoRunArgs(r), "--lib")}
	}
	return r
}

// cargoPackageFor resolves the [package] name owning a repo-relative file by
// walking up from the file's directory to the nearest Cargo.toml that declares
// a package. A workspace-only Cargo.toml (no [package] table) is skipped and
// the walk continues, so a root-level virtual manifest never claims a file its
// member crates own. "" when no package owns the file.
func cargoPackageFor(root, rel string) string {
	dir := filepath.Dir(rel)
	for {
		if name := cargoPackageName(filepath.Join(root, dir, "Cargo.toml")); name != "" {
			return name
		}
		if dir == "." || dir == "" {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// cargoPackageName reads a Cargo.toml's `[package]` name, "" when the file is
// missing or is a virtual (workspace-only) manifest. A line scanner is enough:
// the name key lives directly under [package] in any real manifest, and a
// parse miss only costs the full-suite fallback, never a wrong scope.
func cargoPackageName(manifest string) string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return ""
	}
	inPackage := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inPackage = trimmed == "[package]"
			continue
		}
		if !inPackage {
			continue
		}
		if key, val, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == "name" {
			return strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	return ""
}

// cargoTestTarget maps a repo-relative Rust test path to its cargo test-target
// name: the path component directly under `tests/` — the file's stem for a
// top-level tests/*.rs, the directory name for a nested tests/<dir>/ binary.
// "" when the file is not under a tests/ segment (an inline unit-test module).
func cargoTestTarget(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, seg := range parts {
		if seg == "tests" && i+1 < len(parts) {
			next := parts[i+1]
			if i+1 == len(parts)-1 {
				return strings.TrimSuffix(next, filepath.Ext(next))
			}
			return next
		}
	}
	return ""
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
func narrowToStaged(r Runner, root string, files []string) (Runner, bool) {
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
			dir := filepath.ToSlash(filepath.Dir(f))
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
	case "cargo":
		// Package granularity: each staged file maps to the [package]
		// Cargo.toml that owns it, and the mechanical run tests only those
		// crates. In a Bevy-sized workspace this is the difference between a
		// touched-crate check and rebuilding every test binary in the tree.
		// Any file no package owns keeps the full-suite fallback.
		seen := map[string]bool{}
		var pkgs []string
		for _, f := range files {
			name := cargoPackageFor(root, f)
			if name == "" {
				return r, false
			}
			if !seen[name] {
				seen[name] = true
				pkgs = append(pkgs, name)
			}
		}
		sort.Strings(pkgs)
		args := cargoRunArgs(r)
		for _, p := range pkgs {
			args = append(args, "-p", p)
		}
		return Runner{Cmd: "cargo", Args: args}, true
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

// narrowFailFirstTests scopes the fail-first worktree run to just the staged
// TEST files under judgment, instead of the full unnarrowed suite — on a
// large workspace (e.g. a Bevy monorepo under cargo nextest) the unnarrowed
// run is 10-20 minutes, blows the fail-first stage's own timeout, and proves
// nothing (the stage fails open on the timeout). tests are repo-root-relative
// paths, matching the worktree's layout (a checkout of HEAD).
//
// For cargo: when every staged test file is owned by the SAME [package], the
// result is the exact `-p <pkg> --test <a> --test <b>` argv (nextest vs plain
// `cargo test` preserved via cargoRunArgs), with `--lib` appended when any
// staged test file is an inline #[cfg(test)] module (cargoTestTarget returns
// "" for those — cargoTestTarget only names files under a tests/ segment).
// Staged test files spanning MULTIPLE packages fall back to package
// granularity via the existing narrowToStaged (`-p a -p b`, no --test
// scoping). A file owned by NO package keeps the runner unnarrowed — the
// current full-suite fail-open behavior.
//
// Non-cargo runners defer entirely to narrowToStaged; when it reports no
// related mode (pytest, zig, an unknown command) the runner stays unnarrowed.
func narrowFailFirstTests(r Runner, wt string, tests []string) Runner {
	if r.Cmd != "cargo" {
		if scoped, narrowed := narrowToStaged(r, wt, tests); narrowed {
			return scoped
		}
		return r
	}

	seenPkg := map[string]bool{}
	var pkgs []string
	seenTarget := map[string]bool{}
	var targets []string
	inline := false
	for _, f := range tests {
		name := cargoPackageFor(wt, f)
		if name == "" {
			return r // a file with no owning package → unnarrowed fallback
		}
		if !seenPkg[name] {
			seenPkg[name] = true
			pkgs = append(pkgs, name)
		}
		if tgt := cargoTestTarget(f); tgt != "" {
			if !seenTarget[tgt] {
				seenTarget[tgt] = true
				targets = append(targets, tgt)
			}
		} else {
			inline = true
		}
	}
	if len(pkgs) == 0 {
		return r
	}
	if len(pkgs) > 1 {
		if scoped, narrowed := narrowToStaged(r, wt, tests); narrowed {
			return scoped
		}
		return r
	}

	sort.Strings(targets)
	args := append(cargoRunArgs(r), "-p", pkgs[0])
	for _, tgt := range targets {
		args = append(args, "--test", tgt)
	}
	if inline {
		args = append(args, "--lib")
	}
	return Runner{Cmd: "cargo", Args: args}
}

// narrowSourceEdit builds the related-tests command for a source-file edit,
// dispatching on the detected runner. The npx branch keys off the runner name
// (first arg) because vitest and jest expose different related-tests flags.
// Runners without a related mode return unchanged (full-suite fallback).
func narrowSourceEdit(r Runner, rel, root string) Runner {
	switch r.Cmd {
	case "go":
		return Runner{Cmd: "go", Args: []string{"test", "./" + path.Dir(rel)}}
	case "cargo":
		// Unit tests give the fast per-edit signal; the crate's integration
		// binaries are the commit gate's job. `--lib` on a bin-only crate is
		// an error, not a narrower run, so those keep the full crate suite.
		if _, err := os.Stat(filepath.Join(root, "src", "lib.rs")); err == nil {
			return Runner{Cmd: "cargo", Args: append(cargoRunArgs(r), "--lib")}
		}
		return r
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
