package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner is a test command: a program and its arguments, run from the project
// root. Keeping it a plain value makes detection and narrowing pure and
// testable; only PostToolUse actually executes it.
type Runner struct {
	Cmd  string
	Args []string
	// Dir overrides the execution directory RunSuite uses: "" (the default
	// for every runner except a resolved cargo one) means "use whatever root
	// the caller passed" — RunSuite falls back to its root parameter. Only
	// cargoRunnerAt / precommitRoot's cargo branch set this, to the actual
	// cargo WORKSPACE root: a checked-in .config/nextest.toml and the
	// workspace's Cargo.lock live there, not in a member crate's own
	// directory, so a `-p <pkg>` cargo command must execute from there even
	// though state/mech-cache keys keep using the member crate's own root.
	Dir string
	// Deadline overrides how long RunSuite may run: zero (the default for
	// every runner except one that went through runCargoLocked) means "use
	// RunSuite's own configured timeout unchanged". runCargoLocked sets this
	// to start+stageBudget BEFORE it waits for the machine-wide cargo build
	// lock, so that wait carves OUT of the stage's own budget instead of
	// stacking additively on top of it (RunSuite then runs for whichever is
	// shorter: its configured timeout, or the time remaining until
	// Deadline).
	Deadline time.Time
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
		return cargoTargetRunner(r, rel, root)
	}
	return r
}

// cargoTargetRunner maps a Rust file to the cargo TARGET that actually
// compiles it. Getting this wrong is not a slower run, it is an error: a
// `#[cfg(test)] mod` file under src/ names a target that does not exist, and
// so does an unconfirmed tests/<dir>/ guess -- cargoTestTargetRunner checks
// cargo metadata before trusting a nested candidate.
//
//	<crate>/tests/x.rs          -> --test x
//	<crate>/tests/<dir>/*.rs    -> --test <dir>, confirmed by metadata
//	<crate>/src/a/b_tests.rs    -> --lib, filtered to a::b_tests
//	<crate>/examples/x/*.rs     -> --example x --no-run (see buildonly.go)
//	<crate>/benches/x.rs        -> --bench x --no-run
func cargoTargetRunner(r Runner, rel, root string) Runner {
	if name, nested := cargoTestTarget(rel); name != "" {
		return cargoTestTargetRunner(r, root, name, nested)
	}
	if name := cargoNamedTarget(rel, "examples"); name != "" {
		return cargoTargetArgs(r, root, "--example", name, "--no-run")
	}
	if name := cargoNamedTarget(rel, "benches"); name != "" {
		// A bench RUN (an example's too) costs minutes and says nothing about
		// correctness; the question an edit asks is whether it still compiles.
		return cargoTargetArgs(r, root, "--bench", name, "--no-run")
	}
	if mod := cargoModuleFilterPath(root, rel); mod != "" {
		// The filter dialect follows the RESULTING runner: cargoTargetArgs
		// may upgrade `cargo test` to nextest when the workspace configures
		// it, and only nextest understands -E.
		lib := cargoTargetArgs(r, root, "--lib")
		lib.Args = append(lib.Args, moduleFilterArgs(lib, mod)...)
		return lib
	}
	return cargoTargetArgs(r, root, "--lib")
}

// cargoTargetArgs builds the scoped runner, falling back to the old
// cwd-implicit form when root has no readable [package] manifest.
func cargoTargetArgs(r Runner, root string, extra ...string) Runner {
	if cr, ok := cargoRunnerAt(root, extra...); ok {
		return cr
	}
	return Runner{Cmd: "cargo", Args: append(cargoRunArgs(r), extra...)}
}

// moduleFilterArgs narrows a lib run to one module's tests, in whichever
// dialect the runner speaks: nextest has a filter expression, plain cargo
// test takes a substring.
func moduleFilterArgs(r Runner, mod string) []string {
	if verb := cargoRunArgs(r); len(verb) > 0 && verb[0] == "nextest" {
		return []string{"-E", "test(/^" + mod + "::/)"}
	}
	return []string{mod + "::"}
}

// cargoModulePath turns a src/ path into the Rust module path its tests live
// under, so one edit runs that module's tests instead of the whole lib. The
// crate roots (lib.rs / main.rs) have no submodule, and a file outside src/
// is not a module at all.
func cargoModulePath(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	i := -1
	for n, seg := range parts {
		if seg == "src" {
			i = n
			break
		}
	}
	if i < 0 || i+1 >= len(parts) {
		return ""
	}
	mods := parts[i+1:]
	last := strings.TrimSuffix(mods[len(mods)-1], path.Ext(mods[len(mods)-1]))
	switch last {
	case "lib", "main", "mod":
		mods = mods[:len(mods)-1]
	default:
		mods[len(mods)-1] = last
	}
	return strings.Join(mods, "::")
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

// cargoTomlHasWorkspaceTable reports whether manifest declares a top-level
// [workspace] table. Missing/unreadable manifests report false — the caller
// (cargoWorkspaceRoot) then keeps walking up.
func cargoTomlHasWorkspaceTable(manifest string) bool {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		// Tolerate trailing whitespace/comment after the header
		// ("[workspace]  # root"), not just an exact "[workspace]" line --
		// found in review 2026-08-15: a real-world manifest with either
		// silently fell back to "no workspace found here", so a member
		// crate below it ran unscoped from its own directory instead of the
		// resolved workspace root.
		rest, ok := strings.CutPrefix(trimmed, "[workspace]")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "#") {
			return true
		}
	}
	return false
}

// cargoWorkspaceRoot resolves the actual cargo WORKSPACE root for a cargo
// project root: the nearest ancestor directory (root inclusive) whose
// Cargo.toml declares a [workspace] table. Only there does a checked-in
// .config/nextest.toml live, and the workspace's Cargo.lock is there too —
// a member crate's own directory has neither. A crate with no encompassing
// workspace (no [workspace] table found before the filesystem root) falls
// back to being its own "workspace root".
func cargoWorkspaceRoot(root string) string {
	dir := root
	for {
		if cargoTomlHasWorkspaceTable(filepath.Join(dir, "Cargo.toml")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return root // hit the filesystem root without finding one
		}
		dir = parent
	}
}

// cargoVerbArgs picks nextest vs plain `cargo test` for a cargo WORKSPACE
// root: nextest iff a checked-in <ws>/.config/nextest.toml exists AND the
// cargo-nextest binary is installed — the same rule DetectRunner already
// applies at a project root, generalized to the actual workspace root a
// member crate's tests must be judged from (its own directory very often has
// neither the config file NOR any reason to — only the workspace root does).
func cargoVerbArgs(ws string) []string {
	if _, err := os.Stat(filepath.Join(ws, ".config", "nextest.toml")); err == nil && nextestInstalled() {
		return []string{"nextest", "run"}
	}
	return []string{"test"}
}

// cargoRunnerAt builds the cargo Runner ACTUALLY used to test a cargo
// project root: scoped to its OWN [package] via `-p <pkg>`, executed from
// the resolved workspace root (Dir), with extraArgs appended (`--lib`,
// `--test <name>`, or nothing for a full-crate run). Reports false — a zero
// Runner, meaning "nothing to scope to" — when root has no readable
// [package] Cargo.toml of its own; the caller then keeps its pre-existing
// (unscoped-by-package, cwd-implicit) narrowing instead of losing --lib/
// --test scoping entirely over an unresolvable package name.
func cargoRunnerAt(root string, extraArgs ...string) (Runner, bool) {
	pkg := cargoPackageName(filepath.Join(root, "Cargo.toml"))
	if pkg == "" {
		return Runner{}, false
	}
	ws := cargoWorkspaceRoot(root)
	args := append(cargoVerbArgs(ws), "-p", pkg)
	args = append(args, extraArgs...)
	return Runner{Cmd: "cargo", Args: args, Dir: ws}, true
}

// stagedProjectRoots returns the sorted, deduped set of project roots (per
// FindProjectRoot) that own at least one of the given repo-root-relative
// staged files. A monorepo can stage a cargo crate's tests alongside a
// pytest tool's tests in the SAME commit — grouping by root is what lets
// Precommit judge each toolchain with its own DetectRunner instead of
// whichever marker happens to sit at the outer repo root (the "python commit
// pays a 20-minute cargo build" bug).
func stagedProjectRoots(repoRoot string, files []string) []string {
	seen := map[string]bool{}
	var roots []string
	for _, f := range files {
		root := FindProjectRoot(filepath.Join(repoRoot, f))
		if root == "" {
			continue
		}
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots
}

// filesUnderRoot filters repo-root-relative files to those FindProjectRoot
// resolves to root, preserving input order.
func filesUnderRoot(repoRoot, root string, files []string) []string {
	var out []string
	for _, f := range files {
		if FindProjectRoot(filepath.Join(repoRoot, f)) == root {
			out = append(out, f)
		}
	}
	return out
}

// toRootRelative rewrites repo-root-relative paths to be relative to root
// instead (forward-slashed) — every narrowing helper (go's package dirs,
// cargo's package/test-target lookup, vitest/jest's related-tests args) takes
// paths relative to wherever it will actually run, which is root, not
// necessarily repoRoot. A path that can't be made relative (should not happen
// for a file FindProjectRoot already placed under root) is dropped rather
// than fed to a narrower as a bogus argument.
func toRootRelative(repoRoot, root string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(root, filepath.Join(repoRoot, f))
		if err != nil {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

// cargoPackagesOwning resolves the sorted, deduped set of [package] names
// that own at least one of the given ROOT-RELATIVE files. A file no package
// covers (a virtual workspace manifest, or a path outside any member) is
// silently excluded — an unowned cargo file is never grounds to widen a run
// to the whole workspace; the caller decides whether an empty result means
// "nothing to test" or reports the excluded files itself.
func cargoPackagesOwning(root string, files []string) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, f := range files {
		name := cargoPackageFor(root, f)
		if name == "" {
			continue
		}
		if !seen[name] {
			seen[name] = true
			pkgs = append(pkgs, name)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// cargoAlwaysRunPackages reads the workspace's opted-in always-run packages
// from `[workspace.metadata.aphrollo]`'s `always-run` key in <ws>/Cargo.toml,
// sorted and deduped; empty for any project that never declared one.
//
// A workspace-wide guard package (its tests scan the whole tree rather than
// one crate) is owned by no staged file, so ownership scoping alone runs it
// only when someone edits the guard itself — precisely when its invariant is
// not at risk. The declaration lives in the manifest rather than a tool config
// file so it versions with the code it polices and is reviewed in the same
// diff. A line scanner suffices for the same reason cargoPackageName uses one:
// the key sits directly under its table in any real manifest, and a parse miss
// costs only the pre-existing ownership-scoped run. A `#` comment holding a
// quoted word inside the array is read as a package name; cargo names the bad
// package loudly on the first run.
func cargoAlwaysRunPackages(ws string) []string {
	return cargoAphrolloPackages(ws, "always-run")
}

// cargoClippyCleanPackages reads the packages the workspace declares as
// lint-clean. Only those are gated on `clippy -D warnings` at commit: in a
// large tree most crates carry warnings, so gating all of them is a gate
// nobody can use, while a crate that reached zero must STAY at zero.
func cargoClippyCleanPackages(ws string) []string {
	return cargoAphrolloPackages(ws, "clippy-clean")
}

// cargoAphrolloPackages reads one string-array key from
// `[workspace.metadata.aphrollo]` in <ws>/Cargo.toml, sorted and deduped;
// empty for an absent key or an unreadable manifest.
// cargoAphrolloFlag reads one BOOLEAN key from
// `[workspace.metadata.aphrollo]`. Absent (or unreadable) is false, so a
// workspace that has not opted in never sees the feature at all.
func cargoAphrolloFlag(ws, key string) bool {
	return tomlBoolIn(filepath.Join(ws, "Cargo.toml"), "[workspace.metadata.aphrollo]", key)
}

// cargoAphrolloString reads one scalar STRING key from
// `[workspace.metadata.aphrollo]`.
func cargoAphrolloString(ws, key string) (string, bool) {
	return tomlStringIn(filepath.Join(ws, "Cargo.toml"), "[workspace.metadata.aphrollo]", key)
}

// tomlBoolIn reads one boolean key from one table of a TOML file. A line
// scanner suffices for the same reason cargoPackageName uses one: the key sits
// directly under its table in any real manifest, and a parse miss costs only
// the feature staying off.
func tomlBoolIn(path, table, key string) bool {
	v, _ := tomlBoolSetIn(path, table, key)
	return v
}

// tomlBoolSetIn is tomlBoolIn with the fact tomlBoolIn throws away: whether
// the key was WRITTEN. A default that differs from `false` needs to tell an
// absent key from one somebody set, and only the reader knows.
func tomlBoolSetIn(path, table, key string) (value, set bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	inTable := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		if !inTable {
			continue
		}
		k, val, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(k) == key {
			return strings.TrimSpace(val) == "true", true
		}
	}
	return false, false
}

// tomlStringIn reads one scalar string key from one table of a TOML file,
// quotes stripped. "", false for an absent key or an unreadable manifest —
// same line scanner and same reasoning as tomlBoolIn.
func tomlStringIn(path, table, key string) (value string, set bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	inTable := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		if !inTable {
			continue
		}
		k, val, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(val), `"`), true
		}
	}
	return "", false
}

func cargoAphrolloPackages(ws, key string) []string {
	return tomlStringsIn(filepath.Join(ws, "Cargo.toml"), "[workspace.metadata.aphrollo]", key)
}

// dedupeSorted returns names deduped and sorted, so an identical worktree
// always yields an identical argv — the mech cache keys on that command.
func dedupeSorted(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func narrowFailFirstTests(r Runner, wt string, tests []string) Runner {
	if r.Cmd != "cargo" {
		return narrowNonCargoFailFirst(r, wt, tests)
	}

	seenPkg := map[string]bool{}
	var pkgs []string
	var ownedTests []string
	seenTarget := map[string]bool{}
	var targets []string
	inline := false
	unconfirmed := false
	for _, f := range tests {
		name := cargoPackageFor(wt, f)
		if name == "" {
			continue // unowned file — excluded, never widens the run (fallback removed)
		}
		ownedTests = append(ownedTests, f)
		if !seenPkg[name] {
			seenPkg[name] = true
			pkgs = append(pkgs, name)
		}
		tgt, isInline, isUnconfirmed := cargoFailFirstTarget(wt, name, f)
		switch {
		case isUnconfirmed:
			unconfirmed = true
		case isInline:
			inline = true
		case !seenTarget[tgt]:
			seenTarget[tgt] = true
			targets = append(targets, tgt)
		}
	}
	if len(pkgs) == 0 {
		return r // nothing owned among the staged tests — caller treats as no verdict
	}
	if len(pkgs) > 1 {
		if scoped, narrowed := narrowToStaged(r, wt, ownedTests); narrowed {
			return scoped
		}
		return r
	}

	args := append(cargoRunArgs(r), "-p", pkgs[0])
	if unconfirmed {
		// A staged test's tests/<dir>/ guess is not a real cargo target --
		// naming it is a red on green code, so the whole run drops --test
		// scoping rather than silently excluding that file.
		return Runner{Cmd: "cargo", Args: args}
	}
	sort.Strings(targets)
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
		return narrowGoSourceEdit(r, rel, root)
	case "cargo":
		// Unit tests give the fast per-edit signal; the crate's integration
		// binaries are the commit gate's job. `--lib` on a bin-only crate is
		// an error, not a narrower run, so those keep the full crate suite.
		// cargoRunnerAt resolves the workspace root + `-p <pkg>` scoping
		// (task A4); falls back to the old cwd-implicit `--lib` when root has
		// no readable [package] Cargo.toml of its own.
		if _, err := os.Stat(filepath.Join(root, "src", "lib.rs")); err == nil {
			return cargoTargetRunner(r, rel, root)
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
