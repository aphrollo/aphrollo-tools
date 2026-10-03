package suite

import (
	"reflect"
	"sort"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// A hook that needs the state hash twice in one instant (to identify the tree
// and to key the green) reads the dirty set once.
func TestWorktreeStateHashInBatch_IsTheFreshHashOfTheSameTreeFromOneStatus(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "new.go", "package m\n")
	gitx.BeginHook()

	first := worktreeStateHashInBatch(root)
	second := worktreeStateHashInBatch(root)

	if first == "" || first != second {
		t.Fatalf("batch hashes = %q and %q, want one non-empty hash", first, second)
	}
	if fresh := worktreeStateHash(root); fresh != first {
		t.Errorf("fresh hash %q differs from the batch's %q over the same tree", fresh, first)
	}
	if n := gitx.HookClient(root).Spawns(); n != 2 {
		t.Errorf("%d git spawns for two batch hashes and one fresh one, want 2 (the batch's status and the fresh one)", n)
	}
}

func TestWorktreeStateHash_StagingAChangeDoesNotMoveIt(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "doc.go", "package m\n\nfunc Changed() {}\n")
	write(t, root, "new.go", "package m\n")
	unstaged := worktreeStateHash(root)

	gitDo(t, root, "add", "-A")

	if staged := worktreeStateHash(root); staged != unstaged {
		t.Errorf("staging moved the hash: %q then %q; a green recorded after the edit must hold at the commit that follows", unstaged, staged)
	}
}

func TestWorktreeStateHash_AMovedFileShapesItBySourceAndDestination(t *testing.T) {
	root := makeGoRepo(t)
	clean := worktreeStateHash(root)
	gitDo(t, root, "mv", "doc.go", "moved.go")

	moved := worktreeStateHash(root)
	write(t, root, "moved.go", "package m\n\nfunc Edited() {}\n")

	if moved == "" || moved == clean {
		t.Errorf("a rename left the hash at %q, want it moved from %q", moved, clean)
	}
	if edited := worktreeStateHash(root); edited == moved {
		t.Errorf("editing the rename's destination left the hash at %q", edited)
	}
}

func TestWorktreeStateHash_IsEmptyOutsideARepositoryAndBeforeTheFirstCommit(t *testing.T) {
	if h := worktreeStateHash(t.TempDir()); h != "" {
		t.Errorf("hash outside a repository = %q, want none", h)
	}
	unborn := t.TempDir()
	gitDo(t, unborn, "init", "-q")
	write(t, unborn, "f.go", "package m\n")
	if h := worktreeStateHash(unborn); h != "" {
		t.Errorf("hash of a repository with no commit = %q, want none", h)
	}
}

func TestConfigFiles_ListsDotenvAndConfigDirsAndNothingBuiltOrNested(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".env", "A=1\n")
	write(t, root, "svc/.env.local", "B=2\n")
	write(t, root, "config/feel.ron", "(speed: 1.0)\n")
	write(t, root, "svc/config/deep/x.toml", "x = 1\n")
	write(t, root, "src/main.go", "package main\n")
	write(t, root, "src/configuration/skip.go", "package c\n")
	write(t, root, "target/debug/.env", "built\n")
	write(t, root, "node_modules/pkg/.env", "dep\n")
	write(t, root, ".git/config", "[core]\n")
	write(t, root, "lanes/one/.git", "gitdir: elsewhere\n")
	write(t, root, "lanes/one/.env", "nested\n")

	got := configFiles(root)

	want := []string{".env", "config/feel.ron", "svc/.env.local", "svc/config/deep/x.toml"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configFiles = %q, want %q", got, want)
	}
}
