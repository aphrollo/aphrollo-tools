package tdd

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Scope narrowing: which packages a broad runner is cut down to for the
// files a commit actually staged. Its one recurring hazard is a directory
// that is not a package -- naming one fails the whole run rather than being
// skipped, at both the lint and the test stage.

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
// goPackageDir is the package a staged file belongs to, as a slash path
// relative to root: its own directory when that holds Go files, else the
// nearest ancestor that does. An asset directory is not a package — `go test
// ./internal/tdd/agents` is a setup failure ("no Go files in ..."), not a
// scoped run — and an embedded template belongs to the package whose
// //go:embed directive names it.
func goPackageDir(root, dir string) string {
	rel := filepath.ToSlash(dir)
	for cur := rel; cur != "." && cur != ""; {
		abs := filepath.Join(root, filepath.FromSlash(cur))
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			// Nothing on disk to judge by — a deleted file, or a root this
			// call cannot see. Keep the literal mapping the caller asked for.
			return rel
		}
		if dirHasGoFiles(abs) {
			return cur
		}
		parent := path.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return "."
}

// dirHasGoFiles reports whether dir holds at least one .go file. An unreadable
// directory reads as none, which walks the search one level up rather than
// naming a package that may not exist.
// goDataFileScope maps a NON-Go repo-relative file this repo's own Go tests
// read by literal path (gateOwnInputs' membership already promoted it out of
// Ignore) to the package directory whose tests actually read it. Ordinary
// goPackageDir walks UP from the file's own directory looking for .go files,
// which for .github/workflows/pipeline.yml lands on the module root ("."),
// not internal/tdd — the package pipeline_push_test.go and friends actually
// live in — so without this the mechanical stage would scope to a package
// with nothing testing the file at all (#444). Aphrollo-tools-specific by
// nature (a different repo's Go module has no internal/tdd to point at);
// harmless elsewhere because dirHasGoFiles then filters an entry naming a
// directory that does not exist.
var goDataFileScope = map[string]string{
	".github/workflows/pipeline.yml": "internal/tdd",
}

func dirHasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

func narrowToStaged(r Runner, root string, files []string) (Runner, bool) {
	if len(files) == 0 {
		return r, false
	}
	switch r.Cmd {
	case "go":
		// Dedupe the package dir of each staged file (plus, below, the
		// dirs its reverse dependents add — #399); a root-level file maps to
		// the "." package. Sorted for a deterministic command.
		seen := map[string]bool{}
		var dirs []string
		addDir := func(dir string) {
			// A directory with no .go files is not a package `go test` can
			// load: naming it does not skip it, it fails the run outright
			// with "[setup failed]". The repo root is the case that bites,
			// because a change to aphrollo.toml is Source -- it configures
			// the gate -- while the Go files live under cmd/ and internal/.
			if !dirHasGoFiles(filepath.Join(root, dir)) {
				return
			}
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		for _, f := range files {
			dir, ok := goDataFileScope[filepath.ToSlash(f)]
			if !ok {
				dir = goPackageDir(root, filepath.Dir(f))
			}
			addDir(dir)
		}
		if len(dirs) == 0 {
			// Nothing staged belongs to a Go package, so there is nothing to
			// narrow TO — and nothing to widen from either.
			return r, false
		}
		for _, dep := range goReverseDependents(root, dirs) {
			addDir(dep)
		}
		pkgs := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			pkg := "./" + dir
			if dir == "." {
				pkg = "."
			}
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)
		return Runner{Cmd: "go", Args: append([]string{"test"}, pkgs...)}, true
	case "cargo":
		// Package granularity: each staged file maps to the [package]
		// Cargo.toml that owns it, and the mechanical run tests only those
		// crates. In a Bevy-sized workspace this is the difference between a
		// touched-crate check and rebuilding every test binary in the tree.
		// A file no package owns is EXCLUDED, not a trigger for the
		// full-suite fallback (removed — see cargoPackagesOwning); callers
		// that need to report the excluded files compute that separately.
		pkgs := cargoPackagesOwning(root, files)
		if len(pkgs) == 0 {
			return r, false
		}
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
// scoping), over the OWNED subset only. A file owned by NO package is
// EXCLUDED from judgment, never a trigger to widen the run to the whole
// workspace (that full-suite fallback is removed); when NOTHING staged is
// owned the runner comes back unnarrowed and the caller (failFirstViolatedAt)
// must check ownership itself before ever invoking it, or a fully-unowned
// test set would still run the unscoped full suite.
//
// Non-cargo runners go through narrowNonCargoFailFirst: a Go runner is scoped
// to the staged tests BY NAME (`-run '^(TestA|TestB)$'`, runner_scope_gorun.go)
// when every staged file yields one, else it falls back to narrowToStaged's
// package granularity; when that reports no related mode either (pytest, zig,
// an unknown command) the runner stays unnarrowed.
