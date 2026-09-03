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

// treeStateAt reads one commit or tree's state.
func treeStateAt(repoRoot, rev string) TreeState {
	out, err := git(repoRoot, "ls-tree", "-r", rev)
	if err != nil {
		return TreeState{}
	}
	return treeStateFromListing(out)
}

// treeStateFromListing parses `git ls-tree -r` output into the state a plan
// judges against.
func treeStateFromListing(listing string) TreeState {
	st := TreeState{Blobs: map[string]string{}, Packages: map[string]string{}, TestSets: map[string]string{}}
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

	testBlobs := map[string][]string{}
	for _, e := range entries {
		pkg := packageOf(e.path, pkgDirs)
		st.Packages[e.path] = pkg
		if ClassifyFile(e.path) == Test {
			testBlobs[pkg] = append(testBlobs[pkg], e.path+" "+e.blob)
		}
	}
	for pkg, lines := range testBlobs {
		sort.Strings(lines)
		sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
		st.TestSets[pkg] = hex.EncodeToString(sum[:8])
	}
	return st
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
func PlanDiffFiles(lane []string, now TreeState, cached map[mutantKey]MutantOutcome) []string {
	var out []string
	for _, p := range lane {
		if ClassifyFile(p) == Ignore {
			continue
		}
		if measuredUnchanged(p, now, cached) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// measuredUnchanged reports whether the store already answers for this exact
// file blob, under this exact test set.
func measuredUnchanged(p string, now TreeState, cached map[mutantKey]MutantOutcome) bool {
	blob, testSet := now.Blobs[p], now.TestSets[now.Packages[p]]
	if blob == "" {
		return false
	}
	for _, m := range cached {
		if m.File == p && carriesOver(m, blob, testSet) {
			return true
		}
	}
	return false
}
