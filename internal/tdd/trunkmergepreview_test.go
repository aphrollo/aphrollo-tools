package tdd

import (
	"strings"
	"testing"
)

// TestPrecommit_TrunkMergePreview_BlocksWhenTrunkRenameBreaksLaneMerge pins
// issue #147: a lane commit adding a test that calls Foo() vets clean on its
// OWN tree (Foo still exists there), but trunk has, in the meantime, renamed
// Foo to Bar. Neither branch is individually broken — this is exactly the
// "two branches that each pass alone can still integrate broken" case
// Mechanical's doc comment names, and the ONLY prior evidence for it was
// GitHub's post-push merge-preview CI job. This proves the LOCAL gate now
// catches it before the commit is even made.
func TestPrecommit_TrunkMergePreview_BlocksWhenTrunkRenameBreaksLaneMerge(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)

	trunk := TrunkBranch(root)
	if trunk == "" {
		t.Fatal("setup: could not resolve a trunk branch for the fixture repo")
	}

	// Foo lands on trunk before the lane branches off it.
	write(t, root, "widget.go", "package m\n\nfunc Foo() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "add Foo")

	gitDo(t, root, "checkout", "-b", "lane")

	// Concurrent trunk work renames Foo to Bar — the lane never sees this
	// commit until it merges or rebases.
	gitDo(t, root, "checkout", trunk)
	write(t, root, "widget.go", "package m\n\nfunc Bar() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "rename Foo to Bar")

	// Back on the lane (still has Foo), stage a new test that calls it — this
	// commit's OWN tree vets clean.
	gitDo(t, root, "checkout", "lane")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestFoo(t *testing.T) { Foo() }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("expected the trunk-merge preview to block a commit that vets clean alone but not merged with trunk (%s renamed Foo to Bar), got allowed", trunk)
	}
	if !strings.Contains(res.Message, trunk) {
		t.Fatalf("block message should name the trunk branch (%s) whose merge broke: %s", trunk, res.Message)
	}
}
