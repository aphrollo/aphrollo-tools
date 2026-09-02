package ratchet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Three laws judge a whole TREE rather than a file at a time: the resolved
// dependency graph, one pair of files against each other, and a directory of
// generated JSON. They are answered here instead of in HitsIn, which is
// per-file and pure.

// wholeTreeKinds are the matchers the checker answers itself.
func wholeTreeKind(k MatcherKind) bool {
	switch k {
	case KindRegistryBothWays, KindDepGraphForbids, KindFileSetContainment, KindJSONNumberCeiling:
		return true
	}
	return false
}

// --- dep-graph-forbids ------------------------------------------------------

type cargoDep struct {
	Pkg      string `json:"pkg"`
	DepKinds []struct {
		Kind *string `json:"kind"`
	} `json:"dep_kinds"`
}

type cargoMetadata struct {
	Packages []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"packages"`
	WorkspaceMembers []string `json:"workspace_members"`
	Resolve          struct {
		Nodes []struct {
			ID   string     `json:"id"`
			Deps []cargoDep `json:"deps"`
		} `json:"nodes"`
	} `json:"resolve"`
}

// metadataFixtureFile lets a fixture stand in for a real cargo workspace: the
// law's rule is about the RESOLVED graph, and a checked-in `cargo metadata`
// document is that graph without needing cargo to run over a synthetic tree.
const metadataFixtureFile = "cargo-metadata.json"

// depGraphHits walks the resolved dependency graph from each root and reports
// every forbidden package it can reach, keyed by the PATH that reaches it —
// the path is what a person has to delete an edge from.
func depGraphHits(root string, law Law) ([]Hit, error) {
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
		// A walk that resolved NOTHING satisfies "reaches no forbidden
		// package" perfectly, which is the strongest possible claim over no
		// data at all. Refuse to make it — but under the wildcard a package
		// that depends on nothing is a leaf, not a broken walk, so the floor
		// below is what answers vacuity there.
		if len(paths) == 0 && !wildcard {
			return nil, fmt.Errorf(
				"law %q: %q reaches no dependency at all — the walk is broken, and every clean verdict under it is vacuous",
				law.Name, rootName)
		}
		for _, name := range sortedKeys(paths) {
			if !matchesAny(law.Matcher.Forbidden, name) {
				continue
			}
			hits = append(hits, Hit{
				Law: law.Name, File: rootName + "/Cargo.toml", Weight: 1,
				Key:  paths[name],
				What: fmt.Sprintf("%s reaches %s (%s)", rootName, name, paths[name]),
			})
		}
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

// workspaceRootNames is every package `roots = "*"` stands for: the workspace
// members when cargo named them, and otherwise every package in the document
// (a fixture graph is exactly its workspace).
func workspaceRootNames(meta *cargoMetadata, nameOf map[string]string) []string {
	var out []string
	if len(meta.WorkspaceMembers) > 0 {
		for _, id := range meta.WorkspaceMembers {
			if name, ok := nameOf[id]; ok {
				out = append(out, name)
			}
		}
	} else {
		for _, name := range nameOf {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// depGraphCache is one repo's cached walk: the verdict plus the fingerprint of
// the inputs that can change it.
type depGraphCache struct {
	Fingerprint string `json:"fingerprint"`
	Hits        []Hit  `json:"hits"`
}

func depGraphCachePath(law Law, root string) string {
	if law.CacheDir == "" || law.Name == "" {
		return ""
	}
	return filepath.Join(law.CacheDir, "ratchet-cache", "depgraph-"+law.Name+"-"+cacheKey(root)+".json")
}

func readDepGraphCache(law Law, root, fingerprint string) ([]Hit, bool) {
	path := depGraphCachePath(law, root)
	if path == "" || fingerprint == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c depGraphCache
	if err := json.Unmarshal(data, &c); err != nil || c.Fingerprint != fingerprint {
		return nil, false
	}
	return c.Hits, true
}

func writeDepGraphCache(law Law, root, fingerprint string, hits []Hit) {
	path := depGraphCachePath(law, root)
	if path == "" || fingerprint == "" {
		return
	}
	data, err := json.Marshal(depGraphCache{Fingerprint: fingerprint, Hits: hits})
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// manifestFingerprint hashes every input that can change the resolved graph:
// Cargo.lock and every Cargo.toml, by path, size and mtime. Anything else in
// the tree can move without the graph moving.
func manifestFingerprint(root string) string {
	ignore := loadGitignore(root)
	h := sha256.New()
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			child := path(rel, e.Name())
			if ignore.ignored(child, e.IsDir()) {
				continue
			}
			if e.IsDir() {
				walk(filepath.Join(dir, e.Name()), child)
				continue
			}
			if e.Name() != "Cargo.toml" && e.Name() != "Cargo.lock" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			fmt.Fprintf(h, "%s|%d|%d\n", child, info.Size(), info.ModTime().UnixNano())
		}
	}
	walk(root, "")
	sum := h.Sum(nil)
	if len(sum) == 0 {
		return ""
	}
	return hex.EncodeToString(sum)[:16]
}

// normalEdge reports whether an edge is a NORMAL dependency. A dev or build
// edge never reaches the shipping binary, and following it would blind the
// walk to the distinction the rule is entirely about.
func normalEdge(d cargoDep) bool {
	for _, k := range d.DepKinds {
		if k.Kind == nil {
			return true
		}
	}
	return len(d.DepKinds) == 0
}

// reachablePaths BFSes the graph, recording for each reachable package the
// first (shortest) path that reaches it, rendered `root->a->b`.
func reachablePaths(deps map[string][]string, nameOf map[string]string, rootID, rootName string) map[string]string {
	type step struct {
		id   string
		path string
	}
	seen := map[string]bool{rootID: true}
	out := map[string]string{}
	queue := []step{{rootID, rootName}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		children := append([]string{}, deps[cur.id]...)
		sort.Strings(children)
		for _, child := range children {
			if seen[child] {
				continue
			}
			seen[child] = true
			name := nameOf[child]
			if name == "" {
				name = child
			}
			path := cur.path + "->" + name
			out[name] = path
			queue = append(queue, step{child, path})
		}
	}
	return out
}

// loadCargoMetadata reads the resolved graph: a checked-in document when the
// tree carries one (fixtures), else `cargo metadata` over the tree itself.
func loadCargoMetadata(root string) (*cargoMetadata, error) {
	data, err := os.ReadFile(filepath.Join(root, metadataFixtureFile))
	if err != nil {
		cargo := os.Getenv("CARGO")
		if cargo == "" {
			cargo = "cargo"
		}
		cmd := exec.Command(cargo, "metadata", "--format-version", "1",
			"--manifest-path", filepath.Join(root, "Cargo.toml"))
		out, runErr := cmd.Output()
		if runErr != nil {
			return nil, fmt.Errorf("cargo metadata in %s: %w", root, runErr)
		}
		data = out
	}
	var meta cargoMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("cargo metadata is not valid JSON: %w", err)
	}
	return &meta, nil
}

func matchesAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if matchSegment(p, name) {
			return true
		}
	}
	return false
}

// --- file-set-containment ---------------------------------------------------

// containmentHits reports every capture present in the subset file and absent
// from the superset file. Containment, not equality: a stand-in may refuse
// MORE than the real system, never less.
func containmentHits(root string, law Law) ([]Hit, error) {
	superset, err := captureSet(root, law.Matcher.SupersetFile, law.Matcher.Capture)
	if err != nil {
		return nil, fmt.Errorf("law %q: %w", law.Name, err)
	}
	subset, err := captureSet(root, law.Matcher.SubsetFile, law.Matcher.Capture)
	if err != nil {
		return nil, fmt.Errorf("law %q: %w", law.Name, err)
	}
	// An extractor that quietly matched nothing makes containment vacuous —
	// empty contains empty, permanently and silently green.
	if len(subset) == 0 {
		return nil, fmt.Errorf("law %q: %s yields no capture — the shape the law reads has moved",
			law.Name, law.Matcher.SubsetFile)
	}

	var missing []string
	for _, name := range sortedKeys(subset) {
		if !superset[name] {
			missing = append(missing, name)
		}
	}
	if law.Escape != "" {
		text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(law.Matcher.SupersetFile)))
		if err == nil && strings.Contains(string(text), law.Escape) {
			if len(missing) > 0 {
				return nil, nil // waived, deliberately
			}
			return []Hit{{
				Law: law.Name, File: law.Matcher.SupersetFile, Weight: 1,
				Key:  law.Matcher.SupersetFile + " | stale waiver",
				What: "carries " + law.Escape + " but no longer claims more — the waiver is stale, delete it",
			}}, nil
		}
	}

	var hits []Hit
	for _, name := range missing {
		hits = append(hits, Hit{
			Law: law.Name, File: law.Matcher.SupersetFile, Weight: 1,
			Key:  law.Matcher.SupersetFile + " | " + name,
			What: fmt.Sprintf("missing %q, which %s has", name, law.Matcher.SubsetFile),
		})
	}
	return hits, nil
}

func captureSet(root, file string, pattern *regexp.Regexp) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	out := map[string]bool{}
	for _, m := range pattern.FindAllStringSubmatch(string(data), -1) {
		out[m[len(m)-1]] = true
	}
	return out, nil
}

// --- json-number-ceiling ----------------------------------------------------

// jsonCeilingHits reads one number out of every JSON file the glob names. The
// hit's weight is the measured value (rounded UP — a ceiling), so the baseline
// tightens on an improvement; the TOLERANCE is applied when comparing, not
// here, because a value inside tolerance still has to lower its ceiling.
func jsonCeilingHits(root string, law Law, requireData bool, targetDir string) ([]Hit, error) {
	base, glob, keyPrefix := jsonCeilingBase(root, law.Matcher.Files, targetDir)
	files := globFiles(base, glob)
	// Over the REAL tree an armed law with nothing to read is an error: a
	// clean verdict would be over data that does not exist. Over a FIXTURE it
	// is the point — the clean case proves which files the reader refuses.
	if len(files) == 0 && requireData {
		return nil, fmt.Errorf(
			"law %q: no file matched %q — armed with nothing to read, a clean verdict would be over data that does not exist",
			law.Name, law.Matcher.Files)
	}
	var hits []Hit
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		value, err := jsonNumberAt(data, law.Matcher.JSONPath)
		if err != nil {
			return nil, fmt.Errorf("law %q: %s: %w", law.Name, rel, err)
		}
		hits = append(hits, Hit{
			Law: law.Name, File: keyPrefix + rel, Weight: int(math.Ceil(value)),
			Key:  benchID(keyPrefix + rel),
			What: fmt.Sprintf("%s = %s", law.Matcher.JSONPath, strconv.FormatFloat(value, 'f', -1, 64)),
		})
	}
	return hits, nil
}

// jsonCeilingBase resolves the glob's root and the prefix its findings keep. A
// glob under `target/` reads from targetDir (CARGO_TARGET_DIR) when there is
// one, since that is where the numbers actually land — but the key keeps the
// `target/` prefix either way, or an environment variable would rewrite every
// baseline entry.
func jsonCeilingBase(root, glob, targetDir string) (base, pattern, keyPrefix string) {
	if rest, ok := strings.CutPrefix(glob, "target/"); ok {
		if targetDir == "" {
			targetDir = filepath.Join(root, "target")
		}
		return targetDir, rest, "target/"
	}
	return root, glob, ""
}

// cargoTargetDir is where cargo writes, which CARGO_TARGET_DIR moves.
func cargoTargetDir() string { return os.Getenv("CARGO_TARGET_DIR") }

// globFiles walks base and returns every file matching the glob, sorted.
func globFiles(base, glob string) []string {
	var out []string
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			child := path(rel, e.Name())
			if e.IsDir() {
				walk(filepath.Join(dir, e.Name()), child)
				continue
			}
			if matchGlob(glob, child) {
				out = append(out, child)
			}
		}
	}
	walk(base, "")
	sort.Strings(out)
	return out
}

// benchID names the measurement a file holds: the directory path with the
// generator's own fixed tail (`new/estimates.json`) dropped.
func benchID(rel string) string {
	parts := strings.Split(rel, "/")
	for len(parts) > 0 {
		last := parts[len(parts)-1]
		if last == "new" || last == "base" || strings.HasSuffix(last, ".json") {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	if len(parts) == 0 {
		return rel
	}
	return strings.Join(parts, "/")
}

// jsonNumberAt reads a dotted path (`mean.point_estimate`) out of a JSON
// document and requires it to be a number.
func jsonNumberAt(data []byte, path string) (float64, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("not valid JSON: %w", err)
	}
	cur := doc
	for _, key := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("%s is not an object at %q", path, key)
		}
		cur, ok = obj[key]
		if !ok {
			return 0, fmt.Errorf("%s names no value (%q is absent)", path, key)
		}
	}
	n, ok := cur.(float64)
	if !ok {
		return 0, fmt.Errorf("%s is not a number", path)
	}
	return n, nil
}
