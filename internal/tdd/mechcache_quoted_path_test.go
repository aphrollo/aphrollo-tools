package tdd

import (
	"testing"
)

// TestWorktreeStateHash_MovesWhenANonASCIINamedFileIsEdited pins #175's
// sibling route: git quotes a path containing a byte >= 0x80 as a C-quoted
// string (`"caf\303\251.rs"`) whenever core.quotePath is true, which is
// git's own default (git-config(1)). `git diff --name-only`, `git ls-files
// --others --full-name` and ignoredConfig's `ls-files --full-name` all quote
// on this default, and worktreeStateHash never dequotes, so
// os.Lstat(filepath.Join(base, p)) fails on the quoted literal and the file
// is stamped the constant "gone" no matter what its content is edited to —
// the same cache-poisoning shape #175/91f2099 fixed, reached by a different
// route. core.quotePath is set explicitly here so the test pins the
// behaviour regardless of the developer's global config.
func TestWorktreeStateHash_MovesWhenANonASCIINamedFileIsEdited(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	gitDo(t, root, "config", "core.quotePath", "true")

	write(t, root, "café.rs", "fn one() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "add café.rs")

	write(t, root, "café.rs", "fn two() {}\n")
	before := worktreeStateHash(root)
	if before == "" {
		t.Fatal("no state hash")
	}

	write(t, root, "café.rs", "fn three() {}\n")
	after := worktreeStateHash(root)
	if after == before {
		t.Fatal("a second edit to a tracked, non-ASCII-named file must move the state hash")
	}
}
