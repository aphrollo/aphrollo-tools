package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

func TestRepoID_IsTheNameOfTheStateDirectoryAndSharedByEveryWorktree(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	main := t.TempDir()
	common := filepath.Join(main, ".git")
	gitdir := filepath.Join(common, "worktrees", "lane")
	for _, d := range []string{gitdir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lane := t.TempDir()
	if err := os.WriteFile(filepath.Join(lane, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	want := filepath.Base(core.RepoStateDir(common))
	if got := RepoID(main); got != want {
		t.Errorf("RepoID(main) = %q, want the state dir name %q", got, want)
	}
	if got := RepoID(lane); got != want {
		t.Errorf("RepoID(linked worktree) = %q, want the main repo's %q", got, want)
	}
	if got := RepoID(t.TempDir()); got != "" {
		t.Errorf("RepoID(no repo) = %q, want empty", got)
	}
	if got := RepoID(""); got != "" {
		t.Errorf("RepoID(\"\") = %q, want empty", got)
	}
}
