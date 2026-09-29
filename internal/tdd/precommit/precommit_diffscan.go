package precommit

import "strings"

// suppressionCommitHeader prefixes a commit-time anti-cheat block; the policy's
// own reason (naming the directive and the fix) follows.
const suppressionCommitHeader = "TDD anti-cheat: this commit introduces a suppression that silences a quality gate."

// newSuppression scans the lines this commit INTRODUCES for a suppression and,
// on the first hit in a source/test file, returns the block message. Only
// introduced lines are judged, so a directive that already lived in the file
// does not block an unrelated commit, nor one that moves it. The check masks
// each file's full staged post-image and then restricts to the introduced
// lines, so the masking sees balanced string/comment context and a crafted
// multi-line edit cannot hide a later added directive behind an unbalanced
// opener. Returns "" when nothing blocks.
//
// A line is introduced when its content is new to its file and no code file
// in the commit gave up the same text: every code file's removed
// directives-view texts form one pool, and each removal absorbs at most one
// addition, so a suppression moved between files, or a file renamed, is not
// introduced while a second copy of a moved one still is.
//
// A changed line is not introducing a suppression its old line already
// carried: every code file's removed suppression tokens (directive and named
// code) form a second pool, and a line whose every token that pool holds is
// judged by no suppression policy, so appending a reason to an existing
// lint directive passes while a new code on it, or a new directive, blocks.
func newSuppression(repoRoot string) string {
	type image struct {
		path, pre, post string
		l               lang
		policies        []policy
	}
	var images []image
	pool := map[string]int{}
	directives := map[string]int{}
	for _, path := range stagedPaths(repoRoot) {
		policies := commitSuppressionPolicies(ClassifyFile(path))
		if policies == nil {
			continue
		}
		// Either side is empty where the file is absent: git prints
		// nothing on stdout for a path HEAD or the index does not hold.
		pre, _ := git(repoRoot, "show", "HEAD:"+path)
		post, _ := git(repoRoot, "show", ":"+path)
		l := langOf(path)
		for text, n := range removedTexts(pre, post, l) {
			pool[text] += n
		}
		for token, n := range removedDirectives(pre, post, l) {
			directives[token] += n
		}
		images = append(images, image{path, pre, post, l, policies})
	}
	for _, im := range images {
		introduced := introducedLines(im.pre, im.post, im.l)
		lines := absorbMoved(im.post, im.l, introduced, pool)
		// evaluateAdded honours a policy's per-line escape — currently only
		// error-kind-blind's `// any-error-ok:` — here as at edit time. The
		// lint/type/coverage suppressions carry no escape (see
		// policy_registry_test.go's noEscapeAllowlist), so every one of
		// their lines stays judged.
		covered := coveredDirectives(im.post, im.l, introduced, lines, directives)
		if d := evaluateCovered(im.post, lines, covered, im.l, im.policies, commitPhase); d.Action == Block {
			return suppressionCommitHeader + "\n  " + im.path + ": " + d.Reason
		}
	}
	return ""
}

// stagedPaths lists every path the staged commit changes, in git's sorted
// order, with renames split into the deleted old path and the added new one
// so a renamed file's lines count as removed from one and added to the other.
func stagedPaths(repoRoot string) []string {
	out, err := git(repoRoot, "diff", "--cached", "--name-only", "--no-renames", "-z")
	if err != nil {
		return nil
	}
	return strings.FieldsFunc(out, func(r rune) bool { return r == 0 })
}

// commitSuppressionPolicies is the commit-time gate's policy set for one
// classified file: any code file gets the cross-cutting lint/type/coverage
// suppressions; a test file additionally gets the test-scoped suppressionCat
// warnings (testOracleWarnings — error-kind-blind), which have no meaning in
// source, exactly like the oracleSmells scoping above. nil for anything else,
// so the caller skips it.
func commitSuppressionPolicies(kind Kind) []policy {
	switch kind {
	case Test:
		return concatPolicies(suppressionPolicies, testOracleWarnings)
	case Source:
		return suppressionPolicies
	default:
		return nil
	}
}
