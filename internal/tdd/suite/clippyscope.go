package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

// The workspace check stage compiles what a change can break. It used to do
// that by compiling EVERYTHING -- `cargo clippy --workspace --tests`, 281.9s
// measured on every commit -- most of it crates the change cannot reach.
//
// A change can only break a crate it is upstream of, so the stage is scoped
// to the touched crates plus every crate DOWNSTREAM of them. The clippy-clean
// list does not narrow it: the two lints this stage carries are laws every
// crate owes, and the crate most likely to break under them is the one nobody
// has made warning-free yet. That list gates the separate `-D warnings` stage
// and nothing here. Never `--workspace` either: a stage that compiles the tree
// on every commit is a stage people learn to skip.

// cargoWorkspaceDepsFn reads a workspace's intra-workspace dependency edges
// (package -> the workspace members it depends on). A var so a test can state
// a graph without a cargo run. The error is nil for the two QUIET cases (no
// workspace to ask; a workspace that genuinely has no intra-workspace edges)
// and non-nil only when the graph read itself failed — that is the one case
// clippyScope must not pass through in silence.
var cargoWorkspaceDepsFn = cargoPackageDeps

// cargoWorkspaceDepsAt holds the graphs a test stated for its own root;
// cargoPackageDeps answers from it before it asks cargo.
var cargoWorkspaceDepsAt rootseam.Table[func(root string) (map[string][]string, error)]

// SetCargoWorkspaceDepsForTest replaces cargoWorkspaceDepsFn for a test and returns the restore. A setter
// rather than an assignment, so a test in a package above suite still
// reaches the probe. It is process-wide: a test that calls it runs alone.
func SetCargoWorkspaceDepsForTest(fn func(root string) (map[string][]string, error)) (restore func()) {
	prev := cargoWorkspaceDepsFn
	cargoWorkspaceDepsFn = fn
	return func() { cargoWorkspaceDepsFn = prev }
}

// SetCargoWorkspaceDepsAtForTest states the graph of the workspace at root
// (and of anything under it) for a test and returns the restore. It reaches
// no other root, so the test runs beside the rest.
func SetCargoWorkspaceDepsAtForTest(root string, fn func(root string) (map[string][]string, error)) (restore func()) {
	return cargoWorkspaceDepsAt.Set(root, fn)
}

// clippyScope is the crate list the check stage selects with -p: the touched
// crates, plus every crate that transitively depends on one of them. Sorted,
// so two runs read the same way.
//
// A graph it cannot read falls back to the touched crates alone. That is the
// safe direction in cost, but it is honest only if the narrowing is stated —
// a dependent crate the change can break goes uncompiled, and gateName/
// repoRoot let this print and log exactly that, with the read's own error.
func clippyScope(gateName, repoRoot, ws string, touched []string) []string {
	scope := map[string]bool{}
	for _, p := range touched {
		scope[p] = true
	}
	if len(scope) == 0 {
		return nil
	}
	deps, err := cargoWorkspaceDepsFn(ws)
	if err != nil {
		fmt.Fprintf(rootseam.Stderr(repoRoot),
			"gate %s: check scope → dependency graph unreadable (%v); scoping to the touched crates only, not everything downstream of them\n",
			gateName, err)
		AppendGateLog(gateName, repoRoot, "clippy-scope", "clippy-scope-degraded:"+LogToken(err.Error()), 0)
	}
	for _, p := range dependentsOf(deps, touched) {
		scope[p] = true
	}
	out := make([]string, 0, len(scope))
	for p := range scope {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// cargoOwnTargetDirs are the crate-root directories whose files build only
// the crate's own integration-test, bench and example targets. A dependent
// crate compiles the library alone, so nothing under them reaches it.
var cargoOwnTargetDirs = map[string]bool{"tests": true, "benches": true, "examples": true}

// cargoReachesDependents reports whether a root-relative file can change what
// a crate depending on its owner compiles: false only for a file inside one
// of its crate's own target directories (cargoOwnTargetDirs), true for
// src/, the manifest, a build script and anything else.
func cargoReachesDependents(root, rel string) bool {
	for dir := path.Dir(filepath.ToSlash(rel)); dir != "."; dir = path.Dir(dir) {
		if !cargoOwnTargetDirs[path.Base(dir)] {
			continue
		}
		if cargoPackageName(filepath.Join(root, path.Dir(dir), "Cargo.toml")) != "" {
			return false
		}
	}
	return true
}

// cargoDownstreamScope is the touched crates plus every crate downstream of
// one whose staged files can reach a dependent (cargoReachesDependents).
// rel is root-relative. A commit confined to a crate's own test targets
// takes that crate alone, and asks no dependency graph at all (#870).
func cargoDownstreamScope(gateName, repoRoot, ws, root string, rel []string) []string {
	var reaching []string
	for _, f := range rel {
		if cargoReachesDependents(root, f) {
			reaching = append(reaching, f)
		}
	}
	seeds := cargoPackagesOwning(root, reaching)
	return dedupeSorted(append(clippyScope(gateName, repoRoot, ws, seeds), cargoPackagesOwning(root, rel)...))
}

// dependentsOf walks the graph backwards from seeds: every package that
// reaches one of them through any chain of workspace dependencies.
func dependentsOf(deps map[string][]string, seeds []string) []string {
	reverse := map[string][]string{}
	for pkg, on := range deps {
		for _, d := range on {
			reverse[d] = append(reverse[d], pkg)
		}
	}
	seen := map[string]bool{}
	queue := append([]string{}, seeds...)
	// walk-terminates: a package is queued only when not yet in seen, and each enters seen once
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, up := range reverse[cur] {
			if seen[up] {
				continue
			}
			seen[up] = true
			queue = append(queue, up)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// cargoPackageDeps asks cargo for the workspace's own graph, keyed by PACKAGE
// NAME because that is what `-p` selects (mutants_deps.go asks the same
// question keyed by directory, for a different consumer). `--no-deps`
// resolves nothing from the registry, so it is a manifest read rather than a
// dependency resolution -- and `metadata` is a read-only verb, so the queue
// shim passes it through without taking a build slot.
//
// A nil, nil return means there was nothing to ask (no workspace) or the
// workspace genuinely has no intra-workspace edges — both quiet, both
// legitimate. A non-nil error means the read itself failed (no cargo, a
// broken manifest, unparsable output); the caller must not treat that the
// same as an empty graph, because the two mean opposite things for coverage.
func cargoPackageDeps(ws string) (map[string][]string, error) {
	if ws == "" {
		return nil, nil
	}
	if fn, ok := cargoWorkspaceDepsAt.Get(ws); ok {
		return fn(ws)
	}
	cargo := os.Getenv("CARGO")
	if cargo == "" {
		cargo = "cargo"
	}
	cmd := exec.Command(cargo, "metadata", "--no-deps", "--format-version", "1",
		"--manifest-path", filepath.Join(ws, "Cargo.toml"))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cargo metadata: %w", err)
	}
	return parseWorkspaceDeps(out)
}

// parseWorkspaceDeps reads a `cargo metadata --no-deps` document into the
// intra-workspace edges. With --no-deps the package list IS the workspace
// membership, so an edge naming anything outside it is a registry dependency
// and not a crate -p can select.
func parseWorkspaceDeps(data []byte) (map[string][]string, error) {
	var doc struct {
		Packages []struct {
			Name         string `json:"name"`
			Dependencies []struct {
				Name string `json:"name"`
			} `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cargo metadata output: %w", err)
	}
	members := map[string]bool{}
	for _, p := range doc.Packages {
		members[p.Name] = true
	}
	graph := map[string][]string{}
	for _, p := range doc.Packages {
		var on []string
		for _, d := range p.Dependencies {
			if members[d.Name] && d.Name != p.Name {
				on = append(on, d.Name)
			}
		}
		graph[p.Name] = dedupeSorted(on)
	}
	return graph, nil
}
