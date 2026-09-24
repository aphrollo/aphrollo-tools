package suite

import (
	"path"
	"path/filepath"
)

// unownedEdit names the unit an edited code file would have to sit in for
// the edit hook to have anything to build, when it sits in none: "crate" for
// a cargo runner, "Go package" for a go runner. "" means the file is owned,
// is a build input, or belongs to a runner this does not judge.
//
// Neither runner has a narrower command for such a file, so narrowing fell
// back to the broad one: a scratch script at the root of a virtual cargo
// workspace queued a full-tree `cargo nextest run --no-run` on every write
// (issue #830), and a root script in a Go module whose root holds no .go
// files ran `go test ./...`. Nothing compiles that file, so no run can say
// anything about it.
//
// A build input stays out of this: a file ClassifyFile makes Source without
// a code extension (Cargo.toml, Cargo.lock, go.mod, .cargo/config.toml, the
// gate's own inputs) changes how every unit builds, and build.rs is compiled
// by name.
func unownedEdit(r Runner, target, root string) string {
	// root is the edited file's own project root, so the path is always
	// relative to it; an error leaves rel empty, which is no code file.
	rel, _ := filepath.Rel(root, target)
	rel = filepath.ToSlash(rel)
	if !isCodeFile(rel) || path.Base(rel) == "build.rs" {
		return ""
	}
	switch r.Cmd {
	case "cargo":
		// Only a real workspace manifest at root proves the file is outside
		// every member; an unreadable or table-less one proves nothing.
		if cargoPackageFor(root, rel) == "" && cargoTomlHasWorkspaceTable(filepath.Join(root, "Cargo.toml")) {
			return "crate"
		}
	case "go":
		// A .go file makes its own directory a package, written yet or not.
		if path.Ext(rel) != ".go" && !dirHasGoFiles(filepath.Join(root, goPackageDir(root, path.Dir(rel)))) {
			return "Go package"
		}
	}
	return ""
}
