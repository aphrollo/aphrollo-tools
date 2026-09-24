package suite

import (
	"path/filepath"
	"testing"
)

// The key is taken at the PROJECT root (a workspace member crate), but the
// command it keys runs from the workspace and names every crate downstream
// of the touched one: `cargo test -p core_sim -p lab` compiles
// crates/lab/tests/*.rs, which cargo discovers whether git tracks them or
// not. Untracked files were listed from the crate's own directory only, so a
// new test file beside a downstream crate never moved the key, and a green
// taken before it was written answered for the tree after (#813).
func TestWorktreeStateHash_MovesForAnUntrackedTestOutsideTheProjectRoot(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	write(t, repo, "Cargo.toml", "[workspace]\nmembers = [\"crates/core\", \"crates/lab\"]\n")
	write(t, repo, "crates/core/Cargo.toml", "[package]\nname = \"core_sim\"\n")
	write(t, repo, "crates/core/src/lib.rs", "pub fn step() -> f64 { 1.0 }\n")
	write(t, repo, "crates/lab/Cargo.toml", "[package]\nname = \"lab\"\n")
	write(t, repo, "crates/lab/src/lib.rs", "pub fn pin() -> u64 { 1 }\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "base")
	crate := filepath.Join(repo, "crates", "core")

	before := worktreeStateHash(crate)
	if before == "" {
		t.Fatal("no state hash")
	}
	write(t, repo, "crates/lab/tests/onset.rs", "#[test]\nfn onset() { assert_eq!(lab::pin(), 2); }\n")

	if worktreeStateHash(crate) == before {
		t.Fatal("an untracked test file in a downstream crate is compiled and run by the command this key names, and must move it")
	}
}

// The same holds for the ignored configuration the hash already covers: a
// downstream crate's config/ is read by its tests wherever the key is taken.
func TestWorktreeStateHash_MovesForIgnoredConfigOutsideTheProjectRoot(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	write(t, repo, ".gitignore", "config/\n")
	write(t, repo, "crates/core/Cargo.toml", "[package]\nname = \"core_sim\"\n")
	write(t, repo, "crates/core/src/lib.rs", "pub fn step() -> f64 { 1.0 }\n")
	write(t, repo, "crates/lab/Cargo.toml", "[package]\nname = \"lab\"\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "base")
	write(t, repo, "crates/lab/config/feel.ron", "(speed: 1.0)\n")
	crate := filepath.Join(repo, "crates", "core")

	before := worktreeStateHash(crate)
	if before == "" {
		t.Fatal("no state hash")
	}
	write(t, repo, "crates/lab/config/feel.ron", "(speed: 2.0)\n")

	if worktreeStateHash(crate) == before {
		t.Fatal("ignored configuration a downstream crate's tests read must move the key taken at another crate")
	}
}
