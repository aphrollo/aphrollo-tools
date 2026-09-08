package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
)

// depGraphCeilingEscaped reports whether rootName's own Cargo.toml carries the
// law's escape token in a real TOML comment with a reason after it. That is the
// "escape comment for a deliberate edge, with its reason" the kind's own issue
// promised and #589 found unimplemented — the `escape` field was honoured only
// by file-set-containment, leaving a raised baseline (which the staged-baseline
// guard refuses) as the only way to admit a new edge.
//
// The reason rule is escapeCarriesReason's, the same one every per-file law
// applies: presence of the token alone is not a reviewed decision. Requiring a
// comment keeps the token from forging out of a manifest's own string values
// (a `description = "... crate-fanout-ok: ..."` line).
//
// An escaped root contributes NO hit for that run: it is out of the ceiling,
// not measured at zero. Its reachable packages are still counted toward the
// walk's own vacuity floor, so waiving a root never turns a broken walk into a
// clean verdict.
func depGraphCeilingEscaped(root string, meta *cargoMetadata, rootName string, law Law) bool {
	if law.Escape == "" {
		return false
	}
	data, err := os.ReadFile(depGraphCeilingManifest(root, meta, rootName))
	if err != nil {
		// absence-ok: a manifest this cannot read carries no reviewed reason,
		// so the root stays under the ceiling
		return false
	}
	for _, line := range splitLines(string(data)) {
		if _, comment := splitTrailingComment(line, "#"); escapeCarriesReason(comment, law.Escape) {
			return true
		}
	}
	return false
}

// depGraphCeilingManifest is the Cargo.toml of the package named rootName: the
// path cargo itself reports, or — for a checked-in `cargo metadata` document
// that omits manifest_path, which is how the fixtures work — the
// <root>/<name>/Cargo.toml the hit is already filed under.
func depGraphCeilingManifest(root string, meta *cargoMetadata, rootName string) string {
	for _, p := range meta.Packages {
		if p.Name == rootName && p.ManifestPath != "" {
			return filepath.FromSlash(p.ManifestPath)
		}
	}
	return filepath.Join(root, rootName, "Cargo.toml")
}

// depGraphCeilingHits answers "how much may a root reach at all", the
// complement of dep-graph-forbids' "may it reach THIS one": one hit per
// root, weighted by the count of packages it can reach, so the SAME baseline
// mechanism ceilings the reachable COUNT the way json-number-ceiling
// ceilings a measured number — a raise fails the commit, a fall lowers the
// ceiling, and only an edge moving changes the count (an unrelated reached
// package growing does not). It shares loadCargoMetadata, normalEdge,
// reachablePaths, workspaceRootNames and the depgraph cache with
// dep-graph-forbids — same resolved graph, a different question asked of it.
func depGraphCeilingHits(root string, law Law) ([]Hit, error) {
	fingerprint := manifestFingerprint(root)
	if hits, ok := readDepGraphCache(law, root, fingerprint); ok {
		return hits, nil
	}
	meta, err := loadCargoMetadata(root)
	if err != nil {
		return nil, fmt.Errorf("law %q: %w", law.Name, err)
	}
	nameOf := map[string]string{}
	for _, p := range meta.Packages {
		nameOf[p.ID] = p.Name
	}
	deps := map[string][]string{}
	for _, n := range meta.Resolve.Nodes {
		for _, d := range n.Deps {
			if law.Matcher.Edges == "normal" && !normalEdge(d) {
				continue
			}
			deps[n.ID] = append(deps[n.ID], d.Pkg)
		}
	}

	roots, wildcard := law.Matcher.Roots, false
	if len(roots) == 1 && roots[0] == AllRoots {
		roots, wildcard = workspaceRootNames(meta, nameOf), true
	}
	workspace := map[string]bool{}
	for _, name := range workspaceRootNames(meta, nameOf) {
		workspace[name] = true
	}

	var hits []Hit
	reached := 0
	for _, rootName := range roots {
		id := ""
		for pkgID, name := range nameOf {
			if name == rootName {
				id = pkgID
				break
			}
		}
		if id == "" {
			return nil, fmt.Errorf("law %q: no package named %q in the resolved graph", law.Name, rootName)
		}
		paths := reachablePaths(deps, nameOf, id, rootName)
		reached += len(paths)
		// Same refusal as dep-graph-forbids: a walk that resolved nothing
		// satisfies any ceiling vacuously, which is not a clean verdict —
		// except under the wildcard, where a leaf package legitimately
		// reaches nothing and min_reachable is what answers vacuity instead.
		if len(paths) == 0 && !wildcard {
			return nil, fmt.Errorf(
				"law %q: %q reaches no dependency at all — the walk is broken, and every clean verdict under it is vacuous",
				law.Name, rootName)
		}
		count := 0
		for name := range paths {
			if law.Matcher.Counts == "all" || workspace[name] {
				count++
			}
		}
		if depGraphCeilingEscaped(root, meta, rootName, law) {
			continue
		}
		hits = append(hits, Hit{
			Law: law.Name, File: rootName + "/Cargo.toml", Weight: count,
			Key:  rootName,
			What: fmt.Sprintf("%s reaches %d %s", rootName, count, plural(count, "package")),
		})
	}
	if reached < law.Matcher.MinReachable {
		return nil, fmt.Errorf(
			"law %q: the walk reached %d %s, min_reachable = %d — a verdict over that little data is vacuous",
			law.Name, reached, plural(reached, "package"), law.Matcher.MinReachable)
	}
	if wildcard && reached == 0 {
		return nil, fmt.Errorf(
			"law %q: no package in the workspace reaches anything — the walk is broken, and every clean verdict under it is vacuous",
			law.Name)
	}
	writeDepGraphCache(law, root, fingerprint, hits)
	return hits, nil
}
