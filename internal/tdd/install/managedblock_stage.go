package install

import "strings"

// ManagedBlockRefusal is the commit stage that keeps the committed CLAUDE.md
// managed block equal to the one this binary renders. The block is a function
// of the repo's declarations, so a commit that stages aphrollo.toml can change
// what the block should say while touching no Go file and no CLAUDE.md, and
// nothing else at commit time compares the two (#1014). It answers "" when
// there is nothing to refuse: aphrollo.toml is not staged, the repo keeps no
// CLAUDE.md or none with a managed block, or the block in the index (the
// staged CLAUDE.md, else the committed one) already equals the render. Anything
// else answers the one line naming the command that fixes it.
func ManagedBlockRefusal(repoRoot string) string {
	if repoRoot == "" || !aphrolloTomlStaged(repoRoot) {
		return ""
	}
	indexed, err := gitRead(repoRoot, "show", ":CLAUDE.md")
	if err != nil || !strings.Contains(indexed, claudeMDBegin) {
		return ""
	}
	if _, stale := PatchClaudeMD([]byte(indexed), managedBlockFor(repoRoot)); !stale {
		return ""
	}
	return "aphrollo.toml is staged but the CLAUDE.md managed block differs from the one this build renders for it; " +
		"run `aphrollo install --managed-block-only --repo .` and stage CLAUDE.md"
}

// aphrolloTomlStaged reports whether the commit stages the repo's own
// aphrollo.toml, the file at the root of the checkout.
func aphrolloTomlStaged(repoRoot string) bool {
	out, err := gitRead(repoRoot, "diff", "--cached", "--name-only", "--", "aphrollo.toml")
	if err != nil {
		return false
	}
	for line := range strings.Lines(out) {
		if strings.TrimSpace(line) == "aphrollo.toml" {
			return true
		}
	}
	return false
}
