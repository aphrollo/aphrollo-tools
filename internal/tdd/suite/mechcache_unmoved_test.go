package suite

import "testing"

// A tree that held still from before the run to after it is the tree the run
// compiled, and its green is recorded under that state.
func TestMechCacheAddUnmoved_RecordsTheGreenOfATreeThatHeldStill(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "main.go", "package main\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}

	before := worktreeStateHash(root)
	mechCacheAddUnmoved(root, before, r)

	if !mechCacheHit(mechKey(root, before, r)) {
		t.Fatal("a green over a tree that held still for the whole run must be recorded under that tree's state")
	}
}

// With no state hash there is no tree to vouch for: a root outside any repo
// hashes to "" before and after alike, and that agreement proves nothing.
func TestMechCacheAddUnmoved_RecordsNothingWithoutAStateHash(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	if h := worktreeStateHash(root); h != "" {
		t.Fatalf("setup: the temp dir sits inside a git work tree (state hash %s), so it is no root outside a repo", h)
	}

	mechCacheAddUnmoved(root, "", r)

	if mechCacheHit(mechKey(root, "", r)) {
		t.Fatal("a root with no state hash has no tree a green could be about, and must record none")
	}
}
