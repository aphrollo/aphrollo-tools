package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// A folded test binary broke the path-derived --test guess: cargoTestTarget's
// nested candidate (the directory name under tests/) is only ever a MODULE
// inside a differently-named binary, or a shared helper directory, unless
// cargo metadata actually confirms it. This file is that confirmation --
// never a second heuristic, cargo's own answer.

// cargoTestTargetsFn reads a workspace's real `--test` target names, package
// name -> the set of names cargo metadata reports with kind "test". A var so
// a test can state a workspace's targets without a cargo run.
var cargoTestTargetsFn = loadCargoTestTargets

// cargoTestTargetCache memoizes cargoTestTargetsFn per workspace root for
// this process's lifetime: postedit is one process per edit, so this only
// dedupes the in-process case -- narrowFailFirstTests classifying several
// staged files in the same package during one commit.
var (
	cargoTestTargetCacheMu sync.Mutex
	cargoTestTargetCache   = map[string]map[string]map[string]bool{}
)

func cargoTestTargetsFor(ws string) map[string]map[string]bool {
	cargoTestTargetCacheMu.Lock()
	defer cargoTestTargetCacheMu.Unlock()
	if doc, ok := cargoTestTargetCache[ws]; ok {
		return doc
	}
	doc := cargoTestTargetsFn(ws)
	cargoTestTargetCache[ws] = doc
	return doc
}

// loadCargoTestTargets runs `cargo metadata --no-deps` at the workspace root
// and parses it. nil when there is no cargo, no workspace, or unreadable
// output -- callers then treat every nested candidate as unconfirmed and fall
// back to the package run rather than a guessed --test name.
func loadCargoTestTargets(ws string) map[string]map[string]bool {
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
	return parseCargoTestTargets(out)
}

// parseCargoTestTargets reads a `cargo metadata --no-deps` document into
// package name -> its target names of kind "test" -- the only kind
// `--test <name>` selects.
func parseCargoTestTargets(data []byte) map[string]map[string]bool {
	var doc struct {
		Packages []struct {
			Name    string `json:"name"`
			Targets []struct {
				Name string   `json:"name"`
				Kind []string `json:"kind"`
			} `json:"targets"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	out := map[string]map[string]bool{}
	for _, p := range doc.Packages {
		names := map[string]bool{}
		for _, tgt := range p.Targets {
			for _, k := range tgt.Kind {
				if k == "test" {
					names[tgt.Name] = true
					break
				}
			}
		}
		out[p.Name] = names
	}
	return out
}

// cargoHasTestTarget reports whether cargo metadata confirms pkg declares a
// `--test name` target.
func cargoHasTestTarget(root, pkg, name string) bool {
	targets := cargoTestTargetsFor(cargoWorkspaceRoot(root))
	return targets[pkg][name]
}

// cargoHasTestTargetAt resolves root's own package name and checks it the
// same way, for a caller (cargoTargetRunner) that has not already resolved
// the package.
func cargoHasTestTargetAt(root, name string) bool {
	pkg := cargoPackageName(filepath.Join(root, "Cargo.toml"))
	if pkg == "" {
		return false
	}
	return cargoHasTestTarget(root, pkg, name)
}

// cargoTestTargetRunner builds the scoped runner for a cargoTestTarget
// candidate: trusted directly when it came from cargo's own flat-file
// convention (nested false), confirmed against metadata first when it came
// from a directory guess. An unconfirmed guess falls back to the whole-
// package run -- naming a target that does not exist is a red on green code,
// and the package run is always correct, only broader.
func cargoTestTargetRunner(r Runner, root, name string, nested bool) Runner {
	if nested && !cargoHasTestTargetAt(root, name) {
		return cargoTargetArgs(r, root)
	}
	return cargoTargetArgs(r, root, "--test", name)
}

// cargoFailFirstTarget classifies one staged test file for
// narrowFailFirstTests: target is the confirmed --test name to add ("" for
// both an inline #[cfg(test)] module and an unconfirmed directory guess),
// inline and unconfirmed distinguish which. unconfirmed forces the caller to
// drop --test scoping for the whole package rather than run a narrower set
// that silently excludes the file it could not verify.
func cargoFailFirstTarget(root, pkg, f string) (target string, inline, unconfirmed bool) {
	tgt, nested := cargoTestTarget(f)
	switch {
	case tgt == "":
		return "", true, false
	case nested && !cargoHasTestTarget(root, pkg, tgt):
		return "", false, true
	default:
		return tgt, false, false
	}
}

// cargoNamedTarget names the example/bench a path belongs to: the file stem
// directly under the directory, or the directory name for a multi-file one.
func cargoNamedTarget(rel, dir string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, seg := range parts {
		if seg != dir || i+1 >= len(parts) {
			continue
		}
		next := parts[i+1]
		if i+1 == len(parts)-1 {
			return strings.TrimSuffix(next, path.Ext(next))
		}
		return next
	}
	return ""
}
