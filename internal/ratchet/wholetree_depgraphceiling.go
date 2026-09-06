package ratchet

import (
	"fmt"
)

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
