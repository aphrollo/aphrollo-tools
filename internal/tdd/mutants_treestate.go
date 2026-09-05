package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strings"
)

// The measurement a receipt records comes from the TREE, not from the working
// copy: a blob hash per file and a hash per package over its test files'
// blobs. Reading it from `git ls-tree` means it describes exactly the commit
// the run measured, with no dependence on what is checked out anywhere.

// manifestNames are the files that mark the top of a package. The package
// KEY is that file's directory, relative to the repo root — a string that
// means the same thing in every language and needs nothing but the listing.
var manifestNames = map[string]bool{"Cargo.toml": true, "go.mod": true, "pyproject.toml": true, "package.json": true}

// treeStateAt reads one commit or tree's state, fenced by the workspace's own
// dependency graph.
func treeStateAt(repoRoot, rev string) TreeState {
	out, err := git(repoRoot, "ls-tree", "-r", rev)
	if err != nil {
		return TreeState{}
	}
	return treeStateWithDeps(out, workspaceDepsFn(repoRoot))
}

// treeStateWithDeps parses a listing and fences every package against its own
// files and its dependencies'.
func treeStateWithDeps(listing string, deps map[string][]string) TreeState {
	st := treeStateFromListing(listing)
	st.Fences = fencesFor(st.digests, deps)
	return st
}

// treeStateFromListing parses `git ls-tree -r` output into the state a plan
// judges against.
func treeStateFromListing(listing string) TreeState {
	st := TreeState{Blobs: map[string]string{}, Packages: map[string]string{}, Fences: map[string]string{},
		digests: map[string]packageDigest{}}
	var pkgDirs []string
	type entry struct{ path, blob string }
	var entries []entry

	for line := range strings.SplitSeq(listing, "\n") {
		meta, p, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 3 || f[1] != "blob" {
			continue
		}
		p = strings.Trim(p, `"`)
		st.Blobs[p] = f[2]
		entries = append(entries, entry{path: p, blob: f[2]})
		if manifestNames[path.Base(p)] {
			pkgDirs = append(pkgDirs, packageDirOf(p))
		}
	}
	// Longest first, so the nearest manifest above a file wins.
	sort.Slice(pkgDirs, func(i, j int) bool { return len(pkgDirs[i]) > len(pkgDirs[j]) })

	srcLines, testLines := map[string][]string{}, map[string][]string{}
	for _, e := range entries {
		pkg := packageOf(e.path, pkgDirs)
		st.Packages[e.path] = pkg
		switch ClassifyFile(e.path) {
		case Test:
			testLines[pkg] = append(testLines[pkg], e.path+" "+e.blob)
		case Source:
			srcLines[pkg] = append(srcLines[pkg], e.path+" "+e.blob)
		}
	}
	for pkg := range unionKeys(srcLines, testLines) {
		st.digests[pkg] = packageDigest{Source: digestOf(srcLines[pkg]), Test: digestOf(testLines[pkg])}
	}
	return st
}

// packageDigest is one package's own contribution to a fence, split so a
// dependency contributes its SOURCE only: a dependency's tests do not
// constrain a mutant in the crate that depends on it.
type packageDigest struct{ Source, Test string }

// fencesFor folds each package's own digest together with the source digests
// of every package it transitively depends on. A cycle terminates because a
// package is visited once.
func fencesFor(digests map[string]packageDigest, deps map[string][]string) map[string]string {
	out := map[string]string{}
	for pkg, own := range digests {
		parts := []string{"self " + own.Source + " " + own.Test}
		for _, dep := range transitiveDeps(pkg, deps) {
			parts = append(parts, "dep "+dep+" "+digests[dep].Source)
		}
		out[pkg] = digestOf(parts)
	}
	return out
}

// transitiveDeps is every package reachable from pkg, sorted, and excluding
// pkg itself so a dependency cycle does not fold a package's own tests into
// its fence twice.
func transitiveDeps(pkg string, deps map[string][]string) []string {
	seen := map[string]bool{pkg: true}
	var out []string
	queue := append([]string{}, deps[pkg]...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		out = append(out, cur)
		queue = append(queue, deps[cur]...)
	}
	sort.Strings(out)
	return out
}

// digestOf hashes a set of lines into one short, order-independent string.
func digestOf(lines []string) string {
	if len(lines) == 0 {
		return "-"
	}
	sorted := append([]string{}, lines...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:8])
}

// unionKeys is every key present in any of the maps.
func unionKeys(maps ...map[string][]string) map[string]bool {
	out := map[string]bool{}
	for _, m := range maps {
		for k := range m {
			out[k] = true
		}
	}
	return out
}

// packageDirOf is the directory a manifest declares, "" for one at the root.
func packageDirOf(manifest string) string {
	dir := path.Dir(manifest)
	if dir == "." {
		return ""
	}
	return dir
}

// packageOf names the package a file belongs to: the nearest manifest
// directory at or above it, "" for the repo root's own.
func packageOf(p string, pkgDirs []string) string {
	for _, dir := range pkgDirs {
		if dir == "" {
			continue
		}
		if strings.HasPrefix(p, dir+"/") {
			return dir
		}
	}
	return ""
}

// PlanDiffFiles is the FILE-level half of the incremental plan — what the
// run's `--in-diff` is narrowed to. A file is left out when the store already
// holds a measurement of that exact blob whose package test set has not moved
// since; everything else, including every file nothing has measured, is
// measured again. Files no mutant can live in are never in the run at all.
//
// A file that was measured and yielded NO mutants has nothing in the store, so
// it is measured again — the cost of not being able to tell "measured, none
// found" from "never measured", paid in the safe direction.
//
// repoRoot classifies through classifyRepoPath rather than the bare
// ClassifyFile, which matters for exactly one extension: a .ron resolves its
// owning crate from the filesystem, and the walk has to be anchored at the
// LANE'S OWN repo root — not at whatever directory the measuring process
// happens to have as its current one — or this and laneHasNothingToMutate
// (mutants_carry.go), which already classifies through repoRoot, can decide
// a lane has something to judge when the other decided it did not.
func PlanDiffFiles(repoRoot string, lane []string, now TreeState, cached map[mutantKey]MutantOutcome, producerVersion string) []string {
	var out []string
	for _, p := range lane {
		if classifyRepoPath(repoRoot, p) == Ignore {
			continue
		}
		if measuredUnchanged(p, now, cached, producerVersion) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// measuredUnchanged reports whether the store already answers for this exact
// file blob, behind this exact fence, at the CURRENT producer's version. A
// version mismatch (an upgraded tool, or an old entry stamped before this
// field existed) is read the same as a blob or fence mismatch: the file is
// walked again, once, so a newly added mutator gets its chance (issue #298).
func measuredUnchanged(p string, now TreeState, cached map[mutantKey]MutantOutcome, producerVersion string) bool {
	blob, fence := now.Blobs[p], now.Fences[now.Packages[p]]
	if blob == "" {
		return false
	}
	for _, m := range cached {
		if m.File == p && carriesOver(m, blob, fence) && m.ProducerVersion == producerVersion {
			return true
		}
	}
	return false
}
