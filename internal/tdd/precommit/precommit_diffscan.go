package precommit

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
func newSuppression(repoRoot string) string {
	for _, fa := range stagedAdds(repoRoot) {
		policies := commitSuppressionPolicies(ClassifyFile(fa.Path))
		if policies == nil {
			continue
		}
		post, err := git(repoRoot, "show", ":"+fa.Path)
		if err != nil {
			continue // file not in the index (e.g. deletion) → nothing to judge
		}
		// The diff only names the files; the lines judged are those whose
		// content HEAD's copy of the file did not carry, so a moved or
		// misaligned suppression is never read as introduced. A file absent
		// at HEAD has no pre-image: git prints nothing on stdout, and every
		// line is new.
		pre, _ := git(repoRoot, "show", "HEAD:"+fa.Path)
		l := langOf(fa.Path)
		// evaluateAdded (not the plain evaluateView(addedView(...)) this used
		// to call) so a policy that DOES carry a per-line escape — currently
		// only error-kind-blind's `// any-error-ok:` — is honoured here too,
		// not just at edit time. The legacy lint/type/coverage suppressions
		// carry no escape at all (see policy_registry_test.go's
		// noEscapeAllowlist), so this is unchanged for them: every line stays
		// judged either way.
		if d := evaluateAdded(post, introducedLines(pre, post, l), l, policies, commitPhase); d.Action == Block {
			return suppressionCommitHeader + "\n  " + fa.Path + ": " + d.Reason
		}
	}
	return ""
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
