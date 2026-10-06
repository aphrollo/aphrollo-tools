package merge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCIScratchBase_IsTheCIDirBesideTheLanesOfARepo(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "widgets")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	got := ciScratchBase(repo)

	want := filepath.Join(parent, ".worktrees", "widgets", ".ci")
	if filepath.Clean(got) != want {
		t.Errorf("ciScratchBase = %q, want %q", got, want)
	}
	if st, err := os.Stat(got); err != nil || !st.IsDir() {
		t.Errorf("the base was not made: %v", err)
	}
}

func TestCIScratchBase_WithNoRepoFallsBackToADirBesideTheCheckout(t *testing.T) {
	parent := t.TempDir()
	lane := filepath.Join(parent, "notarepo")
	if err := os.Mkdir(lane, 0o755); err != nil {
		t.Fatal(err)
	}

	got := ciScratchBase(lane)

	if want := filepath.Join(parent, ".aphrollo-ci"); got != want {
		t.Errorf("ciScratchBase = %q, want %q", got, want)
	}
}
