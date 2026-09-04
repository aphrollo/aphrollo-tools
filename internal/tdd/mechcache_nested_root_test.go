package tdd

import (
	"path/filepath"
	"testing"
)

// TestWorktreeStateHash_TrackedFileEditUnderNestedRootMovesHash pins #175:
// root routinely lands on a Cargo workspace member crate's own subdirectory
// (FindProjectRoot finds the nearest Cargo.toml, not the workspace root), and
// an edit to an already-TRACKED file under that nested root must still move
// the state hash. `git diff HEAD --name-only` renders repo-root-relative
// paths regardless of cwd, so joining that path against the nested root
// doubled it into a nonexistent path, os.Lstat failed, and the file's
// contribution to the hash was stamped the constant "gone" no matter what
// changed — a green cached after one edit could be replayed as a hit at
// commit time after a second, breaking edit to the same file (#175).
func TestWorktreeStateHash_TrackedFileEditUnderNestedRootMovesHash(t *testing.T) {
	repo := makeCargoRepo(t)
	nested := filepath.Join(repo, "crates", "foo")
	write(t, nested, "src/main.rs", "fn main() {}\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-q", "-m", "add nested crate")

	// The first edit already puts src/main.rs in `git diff HEAD --name-only`,
	// so BOTH hashes below see the identical path list — isolating the
	// blob-content contribution the bug stamped as the constant "gone"
	// regardless of what changed. Comparing against the pre-edit (unchanged)
	// state would pass even under the bug, since the path's mere PRESENCE
	// in the diff (added between the two states) moves the hash on its own.
	write(t, nested, "src/main.rs", "fn main() { println!(\"one\"); }\n")
	before := worktreeStateHash(nested)
	if before == "" {
		t.Fatal("no state hash")
	}

	write(t, nested, "src/main.rs", "fn main() { println!(\"two\"); }\n")
	after := worktreeStateHash(nested)
	if after == before {
		t.Fatal("a second edit to an already-tracked, already-diffing file under a nested root must move the state hash")
	}
}
