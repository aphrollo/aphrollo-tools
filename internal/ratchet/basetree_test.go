package ratchet

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestGitBaseReader_IgnoresInheritedGitDirReadsRootsOwnTree reproduces the
// nested-git-hook hazard directly: a git hook exports GIT_DIR (and
// GIT_INDEX_FILE) for the repository IT is running against, and a bare
// exec.Command inherits that environment. GIT_DIR takes precedence over
// `-C <dir>`'s own repository discovery, so an unscrubbed gitBaseReader
// asked to read `root` instead silently answers from whatever repo GIT_DIR
// names. Here that is a SECOND repo ("outer", standing in for the hook's
// own repository) with different committed content at the same path as
// "root" ("lane", standing in for the tree gitBaseReader was actually asked
// to read) — so a reader that leaks the environment and one that does not
// are distinguishable by which repo's content comes back.
func TestGitBaseReader_IgnoresInheritedGitDirReadsRootsOwnTree(t *testing.T) {
	isolateGitConfigRatchet(t)

	outer := t.TempDir()
	gitRun(t, outer, "init", "-q", "-b", "main")
	gitRun(t, outer, "config", "user.email", "t@t")
	gitRun(t, outer, "config", "user.name", "t")
	write(t, filepath.Join(outer, "a.txt"), "outer repo content\n")
	gitRun(t, outer, "add", ".")
	gitRun(t, outer, "commit", "-qm", "outer")

	root := t.TempDir()
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	write(t, filepath.Join(root, "a.txt"), "root repo content\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "root")

	// Stand in for a git hook's own exported environment: GIT_DIR/
	// GIT_INDEX_FILE name the OUTER repo, a repository other than root.
	t.Setenv("GIT_DIR", filepath.Join(outer, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(outer, ".git", "index"))

	br := gitBaseReader{root: root, ref: "HEAD"}

	files, err := br.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a.txt"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("List() = %v, want %v (root's own tree, not the inherited GIT_DIR's)", files, want)
	}

	data, err := br.Read("a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "root repo content\n" {
		t.Fatalf("Read(a.txt) = %q, want %q — the inherited GIT_DIR leaked into the read", data, "root repo content\n")
	}

	all, err := br.ReadAll([]string{"a.txt"})
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(all["a.txt"]); got != "root repo content\n" {
		t.Fatalf("ReadAll()[a.txt] = %q, want %q — the inherited GIT_DIR leaked into the read", got, "root repo content\n")
	}
}

// TestGitBaseReader_ListsAndReadsTheRefTree proves gitBaseReader answers from
// the REF, not the worktree: List() names every committed path, sorted, and
// Read() returns the committed blob even when the worktree copy has since
// been dirtied — the whole reason a diff-scoped law can trust "base" to mean
// what was actually committed.
func TestGitBaseReader_ListsAndReadsTheRefTree(t *testing.T) {
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	write(t, filepath.Join(root, "a.txt"), "committed a\n")
	write(t, filepath.Join(root, "sub", "b.txt"), "committed b\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")
	// Dirty the worktree copy after the commit: Read must still serve what
	// was committed, not this.
	write(t, filepath.Join(root, "a.txt"), "dirty worktree copy\n")

	br := gitBaseReader{root: root, ref: "HEAD"}
	files, err := br.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a.txt", "sub/b.txt"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("List() = %v, want %v", files, want)
	}

	data, err := br.Read("a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "committed a\n" {
		t.Errorf("Read(a.txt) = %q, want the committed content, not the dirty worktree copy", data)
	}
}

// TestGitBaseReader_ReadAllReadsEveryPathInOneBatch proves ReadAll reads a
// whole path list through one `git cat-file --batch` process rather than one
// `git show` per path: a path with a space in its name and a path whose
// content carries a NUL byte and a trailing newline both come back
// byte-identical to what was committed, and a path absent from the ref is
// simply absent from the map — not an error.
func TestGitBaseReader_ReadAllReadsEveryPathInOneBatch(t *testing.T) {
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	write(t, filepath.Join(root, "a.txt"), "plain a\n")
	write(t, filepath.Join(root, "path with space.txt"), "spaced content\n")
	write(t, filepath.Join(root, "has-nul.txt"), "before\x00after\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	br := gitBaseReader{root: root, ref: "HEAD"}
	got, err := br.ReadAll([]string{"a.txt", "path with space.txt", "has-nul.txt", "missing.txt"})
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	want := map[string]string{
		"a.txt":               "plain a\n",
		"path with space.txt": "spaced content\n",
		"has-nul.txt":         "before\x00after\n",
	}
	for path, wantContent := range want {
		gotContent, ok := got[path]
		if !ok {
			t.Fatalf("ReadAll()[%q] absent, want %q", path, wantContent)
		}
		if string(gotContent) != wantContent {
			t.Errorf("ReadAll()[%q] = %q, want %q", path, gotContent, wantContent)
		}
	}
	if _, ok := got["missing.txt"]; ok {
		t.Errorf("ReadAll()[%q] present, want absent — not in the ref", "missing.txt")
	}
}
