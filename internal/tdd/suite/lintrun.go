package suite

import (
	"path/filepath"
	"sort"
)

// goLintRunner is the one golangci-lint command a Go root is judged by: the
// commit gate's lint stage runs it, and so does the edit-time run, so a finding
// the run reports is exactly the finding a commit would refuse.
//
// --allow-serial-runners: golangci-lint takes a MACHINE-WIDE lock, not one per
// cache dir, so a second one anywhere on the box makes this one exit 3 with
// "parallel golangci-lint is running" — a rejection that says nothing about the
// code. CI passes it for the same reason; the gate, which runs while other
// sessions build, needs it more.
func goLintRunner(root string, touched []string) Runner {
	return Runner{Cmd: "golangci-lint", Args: append([]string{"run", "--allow-serial-runners"}, touchedGoLintPackages(root, touched)...), Dir: root}
}

// touchedGoLintPackages is the deduped, sorted package set golangci-lint
// scopes to: one ./dir per DISTINCT package a touched file belongs to (goPackageDir
// walks up from a deleted or asset-only path to the nearest real one), "."
// for the root package. Empty touched falls back to ./... — a stage called
// with nothing to scope by (a rename-only or vet-triggered run) must still
// judge the whole module rather than lint zero packages.
func touchedGoLintPackages(root string, touched []string) []string {
	if len(touched) == 0 {
		return []string{"./..."}
	}
	seen := map[string]bool{}
	var pkgs []string
	for _, f := range touched {
		dir := goPackageDir(root, filepath.Dir(f))
		// A touched file whose directory holds no .go files is not a package
		// golangci-lint can load; naming it fails the whole run with
		// "no go files to analyze" and says nothing about the code. The repo
		// root is the case that bites, because a change to aphrollo.toml is
		// Source (it configures the gate) while this module keeps its Go
		// files under cmd/ and internal/.
		if !dirHasGoFiles(filepath.Join(root, dir)) {
			continue
		}
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
	return pkgs
}
