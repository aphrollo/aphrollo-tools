package tdd

import (
	"testing"

	"pgregory.net/rapid"
)

// ignoreOnlyExts are extensions ClassifyFile always reads as Ignore: none of
// them is in sourceExts, none is matched by an isTestFile case (those only
// fire for sourceExts), and — unlike ".md" — none is a //go:embed target
// anywhere in this package (agents.go embeds "agent_*.md"), so a generated
// basename can never accidentally flip to Source through the embed lookup.
var ignoreOnlyExts = []string{".txt", ".json", ".yaml", ".csv", ".png", ".toml"}

// safePathSegment draws a short, plain identifier: letters, digits,
// underscore — enough to vary paths without risking an ignoredDirs segment
// name (node_modules, vendor, testdata, …) or a path-syntax edge case that
// would make the fixture itself ambiguous.
func safePathSegment(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,8}`).Draw(t, label)
}

func ignoreOnlyPath(t *rapid.T, label string) string {
	dir := safePathSegment(t, label+"/dir")
	base := safePathSegment(t, label+"/base")
	ext := rapid.SampledFrom(ignoreOnlyExts).Draw(t, label+"/ext")
	return dir + "/" + base + ext
}

// isDocsOnlyDiff is docsOnly's own condition (docsonly.go), applied directly
// to a path set rather than through a git-staged diff: an EMPTY set is not
// docs-only (there is nothing to say about it, and the ordinary path already
// handles it — mirroring docsOnly's own guard here matters because splitKinds
// alone reads a nil/empty slice as vacuously all-Ignore, which is exactly the
// wrong verdict for "nothing was staged at all"); otherwise, "no path in the
// diff classifies Source or Test".
func isDocsOnlyDiff(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	tests, srcs := splitKinds(paths)
	return len(tests) == 0 && len(srcs) == 0
}

// TestDocsOnlyClassifier_EmptyDiffIsNotDocsOnly pins docsOnly's own stated
// exception: an empty staged set is never read as docs-only, whatever
// splitKinds alone would say about it.
func TestDocsOnlyClassifier_EmptyDiffIsNotDocsOnly(t *testing.T) {
	t.Parallel()
	if isDocsOnlyDiff(nil) {
		t.Fatalf("isDocsOnlyDiff(nil) = true, want false: an empty staged set is not docs-only")
	}
}

// TestDocsOnlyClassifier_AllIgnoreDiffIsDocsOnly is the closed-form half: any
// NON-EMPTY diff built entirely from paths ClassifyFile reads as Ignore is
// docs-only, whatever their number or names — the empty case is its own,
// different claim (TestDocsOnlyClassifier_EmptyDiffIsNotDocsOnly above), not
// this property's "whatever their number".
func TestDocsOnlyClassifier_AllIgnoreDiffIsDocsOnly(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		paths := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string { return ignoreOnlyPath(t, "p") }), 1, 8).Draw(rt, "paths")
		for _, p := range paths {
			if k := ClassifyFile(p); k != Ignore {
				rt.Fatalf("fixture assumption broken: ClassifyFile(%q) = %v, want Ignore", p, k)
			}
		}
		if !isDocsOnlyDiff(paths) {
			rt.Fatalf("an all-Ignore diff %v was not read as docs-only", paths)
		}
	})
}

// TestDocsOnlyClassifier_OneSourceFileFlipsIt: inserting exactly one Source
// path anywhere into an otherwise all-Ignore diff must flip the verdict —
// the property that makes docsOnly's fast path safe to trust: it never skips
// a diff that carries even one line of real code.
func TestDocsOnlyClassifier_OneSourceFileFlipsIt(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		paths := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string { return ignoreOnlyPath(t, "p") }), 0, 8).Draw(rt, "paths")
		insertAt := rapid.IntRange(0, len(paths)).Draw(rt, "insertAt")

		dir := safePathSegment(rt, "srcdir")
		// "_flip" is a fixed, non-empty suffix: whatever safePathSegment
		// drew, the basename below can never end in "_test.go" (Go's own
		// test-file convention), so it is Source by construction — no draw
		// needs to be discarded for colliding with it.
		base := safePathSegment(rt, "srcbase") + "_flip"
		sourcePath := dir + "/" + base + ".go"
		if k := ClassifyFile(sourcePath); k != Source {
			rt.Fatalf("fixture assumption broken: ClassifyFile(%q) = %v, want Source", sourcePath, k)
		}

		withSource := append(append(append([]string{}, paths[:insertAt]...), sourcePath), paths[insertAt:]...)
		if isDocsOnlyDiff(withSource) {
			rt.Fatalf("adding one Source path %q at index %d did not flip docs-only: diff = %v", sourcePath, insertAt, withSource)
		}
	})
}
