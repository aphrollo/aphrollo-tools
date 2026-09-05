package ratchet

import (
	"path/filepath"
	"reflect"
	"testing"
)

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
