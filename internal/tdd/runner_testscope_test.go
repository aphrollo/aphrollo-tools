package tdd

import (
	"slices"
	"testing"
)

// The same rule the lint scope needed, at the other stage. aphrollo.toml is
// Source so a change to the gate's own config cannot take the docs-only fast
// path, and that puts the repo ROOT into the package list. This module keeps
// no .go files there, and `go test .` on such a package does not skip it, it
// FAILS:
//
//	FAIL	.	[setup failed]
//
// which rejected a lane merge for a reason that was not about the code.
func TestNarrowToStaged_SkipsAPackageDirectoryWithNoGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\n")
	write(t, root, "internal/x/x.go", "package x\n")

	got, ok := narrowToStaged(Runner{Cmd: "go"}, root, []string{"aphrollo.toml", "internal/x/x.go"})

	if !ok {
		t.Fatal("narrowToStaged declined to narrow; the staged set names a real package")
	}
	if slices.Contains(got.Args, ".") {
		t.Errorf("args = %q, must not name the root — it holds no .go files and `go test .` fails there", got.Args)
	}
	if want := []string{"test", "./internal/x"}; !slices.Equal(got.Args, want) {
		t.Errorf("args = %q, want %q", got.Args, want)
	}
}

// A root that DOES hold Go files is still tested: the rule is "no Go files
// here", not "never the root".
func TestNarrowToStaged_StillNamesARootThatHoldsGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")

	got, ok := narrowToStaged(Runner{Cmd: "go"}, root, []string{"main.go"})
	if !ok || !slices.Contains(got.Args, ".") {
		t.Errorf("args = %q (ok=%v), want the root named — it holds Go files", got.Args, ok)
	}
}

// And when nothing staged belongs to a Go package there is nothing to narrow
// to: the caller must fall back to its unnarrowed runner rather than be handed
// a `go test` with no packages, which tests the current directory and fails
// the same way.
func TestNarrowToStaged_DeclinesWhenNoStagedFileHasAGoPackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\n")

	if got, ok := narrowToStaged(Runner{Cmd: "go"}, root, []string{"aphrollo.toml"}); ok {
		t.Errorf("narrowToStaged narrowed to %q; with no Go package staged it must decline", got.Args)
	}
}

// A staged .github/workflows/pipeline.yml maps to internal/tdd, not to the
// package goPackageDir would otherwise land on by walking UP from
// .github/workflows looking for .go files (the module root, "."): pipeline.yml
// is not Go source, and nothing under .github lives inside internal/tdd's own
// directory tree, so the ordinary per-file walk cannot reach the package whose
// tests actually read it. Without goDataFileScope the mechanical stage would
// scope to "." (or decline entirely, if the root holds no .go files either)
// and never run the package that pins pipeline.yml's contents (#444).
func TestNarrowToStaged_MapsThePipelineWorkflowToInternalTDD(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, ".github/workflows/pipeline.yml", "jobs: {}\n")
	write(t, root, "internal/tdd/x.go", "package tdd\n")
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")

	got, ok := narrowToStaged(Runner{Cmd: "go"}, root, []string{".github/workflows/pipeline.yml"})

	if !ok {
		t.Fatal("narrowToStaged declined to narrow; pipeline.yml maps to a real package")
	}
	if want := []string{"test", "./internal/tdd"}; !slices.Equal(got.Args, want) {
		t.Errorf("args = %q, want %q — a pipeline.yml-only change must scope to the package that reads it, not the module root", got.Args, want)
	}
}
