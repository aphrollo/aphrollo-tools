package tdd

import (
	"strings"
	"testing"
)

// A `git merge --no-ff lane/<x>` into the merge-only primary leaves its own
// auto-merged (or conflict-resolved) paths STAGED — that IS the merge, not
// some other session's work. foreignStagedLine used to read every staged
// path the same way regardless of MERGE_HEAD, so the merge's own command
// was told its own files were "another session's work" (issue #713). When
// MERGE_HEAD exists, the staged paths this call surfaces are the merge in
// progress and must never be attributed to a foreign session.
func TestPostBash_MergeInProgressPrimary_NeverReadsItsOwnStagedPathsAsAnotherSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)

	// Diverge lane and primary on the same file so the merge conflicts.
	write(t, lane, "doc.go", "package m\n\nfunc Lane() int { return 1 }\n")
	gitDo(t, lane, "add", "doc.go")
	gitDo(t, lane, "commit", "-qm", "lane change")

	write(t, primary, "doc.go", "package m\n\nfunc Primary() int { return 2 }\n")
	gitDo(t, primary, "add", "doc.go")
	gitDo(t, primary, "commit", "-qm", "primary change")

	cmd := "git merge --no-ff lane/x"
	PreBash(bashPayload(t, "s713", primary, cmd))

	// Real conflicted merge: git() (unlike gitDo) tolerates the non-zero exit.
	if _, err := git(primary, "merge", "--no-ff", "lane/x"); err == nil {
		t.Fatal("setup: expected the merge to conflict, but it succeeded cleanly")
	}
	if _, err := git(primary, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err != nil {
		t.Fatal("setup: expected MERGE_HEAD after the conflicting merge")
	}
	// Resolve and stage the merge's own conflict, matching what a real
	// `git merge --no-ff` leaves behind.
	write(t, primary, "doc.go", "package m\n\nfunc Merged() int { return 3 }\n")
	gitDo(t, primary, "add", "doc.go")

	var dirs []string
	text := PostBash(bashPayload(t, "s713", primary, cmd), recordSuiteDirs(&dirs))

	if strings.Contains(text, "another session's") {
		t.Fatalf("a merge's own staged paths must never read as another session's work, got %q", text)
	}
	if !strings.Contains(text, "merge in progress") {
		t.Fatalf("expected a true merge-in-progress line, got %q", text)
	}
}
