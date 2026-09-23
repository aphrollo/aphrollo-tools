package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// An [[example]] or [[bench]] target may declare `required-features`, and
// cargo refuses to build such a target without them: "target `elem_tire_rig`
// in package `forge` requires the features: `debug-render`, `free-camera`".
// The edit hook's compile check for an example or bench built it bare, so
// every edit to that file, a doc comment included, reported a red the code
// did not earn (issue #755). The features come from cargo metadata, cargo's
// own reading of the manifest, never from a second parse of it.

// cargoTarget names one target of a package the way cargo metadata does: its
// kind ("example", "bench", …) and its name.
type cargoTarget struct{ kind, name string }

// cargoRequiredFeaturesFn reads a workspace's required features per target:
// package name -> target -> features. A var so a test can state a
// workspace's targets without a cargo run.
var cargoRequiredFeaturesFn = loadCargoRequiredFeatures

// loadCargoRequiredFeatures runs `cargo metadata --no-deps` at the workspace
// root and parses it. nil when there is no cargo or unreadable output: the
// target is then built bare, exactly as before.
func loadCargoRequiredFeatures(ws string) map[string]map[cargoTarget][]string {
	out, ok := cargoMetadataNoDeps(ws)
	if !ok {
		return nil
	}
	return parseCargoRequiredFeatures(out)
}

// parseCargoRequiredFeatures reads a `cargo metadata --no-deps` document into
// package name -> target -> that target's required features.
func parseCargoRequiredFeatures(data []byte) map[string]map[cargoTarget][]string {
	var doc struct {
		Packages []struct {
			Name    string `json:"name"`
			Targets []struct {
				Name     string   `json:"name"`
				Kind     []string `json:"kind"`
				Features []string `json:"required-features"`
			} `json:"targets"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	out := map[string]map[cargoTarget][]string{}
	for _, p := range doc.Packages {
		targets := map[cargoTarget][]string{}
		for _, tgt := range p.Targets {
			for _, k := range tgt.Kind {
				targets[cargoTarget{k, tgt.Name}] = tgt.Features
			}
		}
		out[p.Name] = targets
	}
	return out
}

// withRequiredFeatures adds `--features` naming the features the kind/name
// target of root's package requires, and returns r unchanged when it
// requires none.
func withRequiredFeatures(r Runner, root, kind, name string) Runner {
	pkg := cargoPackageName(filepath.Join(root, "Cargo.toml"))
	features := cargoRequiredFeaturesFn(cargoWorkspaceRoot(root))[pkg][cargoTarget{kind, name}]
	if len(features) == 0 {
		return r
	}
	r.Args = append(append([]string{}, r.Args...), "--features", strings.Join(features, ","))
	return r
}
