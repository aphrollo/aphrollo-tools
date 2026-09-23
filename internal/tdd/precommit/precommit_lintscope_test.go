package precommit

import (
	"slices"
	"testing"
)

// aphrollo.toml is classified as Source so a change to the gate's own config
// cannot take the docs-only fast path. That put the repo ROOT into the lint
// scope, and this module keeps no .go files there — everything lives under
// cmd/ and internal/. golangci-lint refuses such a scope outright:
//
//	Running error: context loading failed: failed to load packages: failed to
//	load packages: package .: no go files to analyze
//
// so every commit touching aphrollo.toml was rejected by a stage that had
// nothing to say about the code.
func TestTouchedGoLintPackages_SkipsADirectoryWithNoGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\n")
	write(t, root, "internal/x/x.go", "package x\n")

	got := touchedGoLintPackages(root, []string{"aphrollo.toml", "internal/x/x.go"})

	if slices.Contains(got, ".") {
		t.Errorf("scope = %q, must not include the root — it holds no .go files and golangci-lint refuses it", got)
	}
	if want := []string{"./internal/x"}; !slices.Equal(got, want) {
		t.Errorf("scope = %q, want %q", got, want)
	}
}

// ...and a root that DOES hold Go files is still linted: the rule is "no Go
// files here", not "never the root".
func TestTouchedGoLintPackages_StillScopesARootThatHoldsGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")

	if got := touchedGoLintPackages(root, []string{"main.go"}); !slices.Contains(got, ".") {
		t.Errorf("scope = %q, want the root included — it holds Go files", got)
	}
}

// A change that touches nothing lintable at all must lint NOTHING, not fall
// back to the whole module: the fallback exists for a run called with no
// scope, and turning a config-only commit into a full-module lint is how a
// cheap stage becomes the slowest one.
func TestTouchedGoLintPackages_LintsNothingWhenNoTouchedFileHasAGoPackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\n")

	if got := touchedGoLintPackages(root, []string{"aphrollo.toml"}); len(got) != 0 {
		t.Errorf("scope = %q, want empty — nothing touched belongs to a Go package", got)
	}
}
