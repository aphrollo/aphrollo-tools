package tdd

import (
	"strings"
	"testing"
)

// This repo's CLAUDE.md carries the managed block, committed, and every box
// that runs `aphrollo install` here re-renders it (issue #874). A committed
// block that differs from the render dirties the tracked file on the next
// install, and nothing noticed when it drifted: the committed copy had lost
// two bullets the template had grown and still carried one box's shim path.
// The block names one fact about the install that wrote it, whether the
// queue shims are on the agent's PATH (#889), so the committed block must be
// exactly one of the two renders, with or without the shims; only line
// endings are normalized, for a Windows checkout.
func TestOwnClaudeMD_CarriesTheBlockThisBuildRenders(t *testing.T) {
	t.Parallel()
	root := repoRootForTest(t)
	text := strings.ReplaceAll(repoFile(t, "CLAUDE.md"), "\r\n", "\n")
	const begin, end = "<!-- aphrollo:begin -->", "<!-- aphrollo:end -->\n"
	i, j := strings.Index(text, begin), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatal("CLAUDE.md has no managed block between the aphrollo markers")
	}
	have := text[i : j+len(end)]
	if want := managedBlockFor(root, true); have != want && have != managedBlockFor(root, false) {
		t.Errorf("CLAUDE.md's managed block differs from the one this build renders; run `aphrollo install --repo <lane>` "+
			"and commit the result, never hand-edit the block:\n--- committed\n%s\n--- rendered\n%s", have, want)
	}
}
