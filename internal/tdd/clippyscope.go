package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
// a graph without a cargo run.
var cargoWorkspaceDepsFn = cargoWorkspaceDeps

// clippyScope is the crate list the check stage selects with -p: the touched
// crates, plus every crate that transitively depends on one of them. Sorted,
// so two runs read the same way.
//
// A graph it cannot read falls back to the touched crates alone. That is the
// safe direction in cost and the honest one in coverage: it under-covers
// loudly rather than quietly reinstating a whole-workspace compile.
func clippyScope(ws string, touched []string) []string {
	scope := map[string]bool{}
	for _, p := range touched {
		scope[p] = true
	}
	if len(scope) == 0 {
		return nil
	}
	for _, p := range dependentsOf(cargoWorkspaceDepsFn(ws), touched) {
		scope[p] = true
	}
	out := make([]string, 0, len(scope))
	for p := range scope {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
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

// cargoWorkspaceDeps asks cargo for the workspace's own graph. `--no-deps`
// resolves nothing from the registry, so it is a manifest read rather than a
// dependency resolution -- and `metadata` is a read-only verb, so the queue
// shim passes it through without taking a build slot. nil when there is no
// cargo, no workspace, or unreadable output: the caller then scopes to the
// touched crates alone.
func cargoWorkspaceDeps(ws string) map[string][]string {
	if ws == "" {
		return nil
	}
	cargo := os.Getenv("CARGO")
	if cargo == "" {
		cargo = "cargo"
	}
	cmd := exec.Command(cargo, "metadata", "--no-deps", "--format-version", "1",
		"--manifest-path", filepath.Join(ws, "Cargo.toml"))
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseWorkspaceDeps(out)
}

// parseWorkspaceDeps reads a `cargo metadata --no-deps` document into the
// intra-workspace edges. With --no-deps the package list IS the workspace
// membership, so an edge naming anything outside it is a registry dependency
// and not a crate -p can select.
func parseWorkspaceDeps(data []byte) map[string][]string {
	var doc struct {
		Packages []struct {
			Name         string `json:"name"`
			Dependencies []struct {
				Name string `json:"name"`
			} `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
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
	return graph
}
