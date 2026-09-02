package tdd

import (
	"testing"
)

// TestWorktreeStateHash_SeesIgnoredConfig pins the hole opened by sharing the
// green cache across a repo's worktrees: the hash covered TRACKED content
// only, and the suites also read ignored configuration (.env, a crate's
// config/*.ron). Two lanes with identical tracked files and different config
// are not the same proven fact, and one used to inherit the other's green.
func TestWorktreeStateHash_SeesIgnoredConfig(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", ".env\ntarget/\n")
	write(t, root, "config/feel.ron", "(speed: 1.0)\n")
	write(t, root, "main.go", "package main\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "init")

	before := worktreeStateHash(root)
	if before == "" {
		t.Fatal("no state hash")
	}

	write(t, root, ".env", "DATABASE_URL=postgres://one\n")
	after := worktreeStateHash(root)
	if after == before {
		t.Fatal("an ignored .env the suite reads must move the state hash")
	}

	// A huge ignored build dir is NOT configuration: hashing it would cost
	// minutes per commit for a fact that changes on every build.
	write(t, root, "target/debug/big.rlib", "0123456789\n")
	if worktreeStateHash(root) != after {
		t.Fatal("build output must not move the state hash")
	}
}
