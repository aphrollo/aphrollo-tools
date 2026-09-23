package suite

import (
	"path/filepath"
	"testing"
)

// TestMechKey_SharedAcrossWorktreesOfOneRepo pins the key's identity: two
// linked worktrees of ONE repo with IDENTICAL content and the IDENTICAL test
// command describe the same proven-green fact, so a lane must reuse main's
// result instead of paying for the same suite again. Keying on the worktree
// root made every lane cold — the same waste the shared gate target dir
// fixes for artifacts.
func TestMechKey_SharedAcrossWorktreesOfOneRepo(t *testing.T) {
	repo := makeCargoRepo(t)
	lane := filepath.Join(t.TempDir(), "lane-x")
	gitDo(t, repo, "worktree", "add", "-b", "lane/x", lane)

	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "m"}}
	if mechKey(repo, "content-hash", r) != mechKey(lane, "content-hash", r) {
		t.Fatal("two worktrees of one repo with identical content and command must share one cache key")
	}
}

// TestMechKey_DistinctPerRepoContentAndCommand pins what must still key
// apart: a DIFFERENT repo (its own history and files), different worktree
// content, and a differently-scoped command are three different facts, and
// none of them may satisfy a lookup for another.
func TestMechKey_DistinctPerRepoContentAndCommand(t *testing.T) {
	repoA := makeCargoRepo(t)
	repoB := makeCargoRepo(t)
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "m"}}

	if mechKey(repoA, "h", r) == mechKey(repoB, "h", r) {
		t.Error("two different repos must never share a cache key")
	}
	if mechKey(repoA, "h1", r) == mechKey(repoA, "h2", r) {
		t.Error("different worktree content must never share a cache key")
	}
	scoped := Runner{Cmd: "cargo", Args: []string{"test", "-p", "other"}}
	if mechKey(repoA, "h", r) == mechKey(repoA, "h", scoped) {
		t.Error("a differently-scoped command must never satisfy another's lookup")
	}
}

// TestMechKey_NonRepoRootStillKeysOnItself pins the fallback: a project root
// that is not inside a git repo has no common dir to key on, so it keys on
// itself rather than collapsing every such root into one shared key.
func TestMechKey_NonRepoRootStillKeysOnItself(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	if mechKey(a, "h", r) == mechKey(b, "h", r) {
		t.Fatal("two unrelated non-repo roots must not share a cache key")
	}
}
