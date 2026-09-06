package tdd

import (
	"errors"
	"slices"
	"testing"
)

// stubGoWorkspaceGraph states a Go module's intra-module dependency edges
// without a real `go list` run: pkg dir -> the package dirs it imports.
// Mirrors stubWorkspaceGraph in clippyscope_test.go for the cargo graph.
func stubGoWorkspaceGraph(t *testing.T, graph map[string][]string) {
	t.Helper()
	prev := goWorkspaceDepsFn
	goWorkspaceDepsFn = func(string) (map[string][]string, error) { return graph, nil }
	t.Cleanup(func() { goWorkspaceDepsFn = prev })
}

func stubGoWorkspaceGraphError(t *testing.T, err error) {
	t.Helper()
	prev := goWorkspaceDepsFn
	goWorkspaceDepsFn = func(string) (map[string][]string, error) { return nil, err }
	t.Cleanup(func() { goWorkspaceDepsFn = prev })
}

// #399: a change under ratchet broke tests in cli, an IMPORTER of ratchet
// that the touched-packages-only scope never ran. goReverseDependents must
// widen a touched package to everything that (transitively) imports it.
func TestGoReverseDependents_WidensToEveryTransitiveImporter(t *testing.T) {
	// cli -> ratchet, docs -> ratchet, tdd -> docs; aside depends on nothing
	// that moved.
	stubGoWorkspaceGraph(t, map[string][]string{
		"internal/cli":     {"internal/ratchet"},
		"internal/docs":    {"internal/ratchet"},
		"internal/tdd":     {"internal/docs"},
		"internal/ratchet": nil,
		"internal/aside":   nil,
	})

	got := goReverseDependents(t.TempDir(), []string{"internal/ratchet"})

	want := []string{"internal/cli", "internal/docs", "internal/tdd"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("goReverseDependents(ratchet) = %q, want %q — cli imports ratchet directly and tdd reaches it through docs", got, want)
	}
	if slices.Contains(got, "internal/aside") {
		t.Errorf("goReverseDependents(ratchet) = %q, must not include a package that does not depend on it", got)
	}
}

// The repo root package is keyed "." by goPackageDir/narrowToStaged but ""
// by goWorkspaceDeps (mutants_deps.go's TreeState convention); this is the
// one widening that must reconcile the two without either side changing its
// own convention.
func TestGoReverseDependents_ReconcilesTheRootPackageKeyConvention(t *testing.T) {
	stubGoWorkspaceGraph(t, map[string][]string{
		"internal/cli": {""}, // cmd/... style root package, keyed "" here
	})

	got := goReverseDependents(t.TempDir(), []string{"."})

	if want := []string{"internal/cli"}; !slices.Equal(got, want) {
		t.Errorf("goReverseDependents(\".\") = %q, want %q", got, want)
	}
}

// The exclusion is real, not decorative: internal/workspace must never be
// ADDED by the widening, however many hops it sits from a touched package —
// see goReverseScopeExclude's own comment for the measured reason.
func TestGoReverseDependents_NeverAddsAnExcludedPackage(t *testing.T) {
	stubGoWorkspaceGraph(t, map[string][]string{
		"internal/workspace": {"internal/tdd"},
		"internal/cli":       {"internal/tdd"},
	})

	got := goReverseDependents(t.TempDir(), []string{"internal/tdd"})

	if want := []string{"internal/cli"}; !slices.Equal(got, want) {
		t.Errorf("goReverseDependents(tdd) = %q, want %q — internal/workspace is excluded even though it directly imports tdd", got, want)
	}
}

// A graph the probe could not read (no `go list`, a broken module) widens to
// NOTHING — the safe direction in cost — rather than treating a read failure
// as "no dependents".
func TestGoReverseDependents_UnreadableGraphWidensToNothing(t *testing.T) {
	stubGoWorkspaceGraphError(t, errors.New("go list: no such tool"))

	if got := goReverseDependents(t.TempDir(), []string{"internal/ratchet"}); got != nil {
		t.Errorf("goReverseDependents with an unreadable graph = %q, want nil", got)
	}
}

// The consequence asserted where it actually bites: narrowToStaged's go case
// must fold the reverse-dependents widening into the `go test` argv it hands
// back, not just expose it as an unused helper.
func TestNarrowToStaged_Go_WidensToReverseDependents(t *testing.T) {
	root := t.TempDir()
	write(t, root, "internal/ratchet/preset.go", "package ratchet\n")
	write(t, root, "internal/cli/cli.go", "package cli\n")
	stubGoWorkspaceGraph(t, map[string][]string{
		"internal/cli": {"internal/ratchet"},
	})

	got, ok := narrowToStaged(Runner{Cmd: "go"}, root, []string{"internal/ratchet/preset.go"})

	if !ok {
		t.Fatal("narrowToStaged declined to narrow; the staged file names a real package")
	}
	want := []string{"test", "./internal/cli", "./internal/ratchet"}
	if !slices.Equal(got.Args, want) {
		t.Errorf("args = %q, want %q — a ratchet change must also run its importer cli's suite", got.Args, want)
	}
}
