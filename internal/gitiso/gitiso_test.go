package gitiso

import (
	"os"
	"path/filepath"
	"testing"
)

// The probe is the child half of VerifyNoLeak.
func TestGitIsolation_Probe(t *testing.T) { Probe(t) }

// A hook exports GIT_DIR and friends, and a run can start inside a repository
// or with its temp dir in one; Main is what keeps the probe's bare git calls
// off all of it.
func TestMain_KeepsBareGitCallsOffTheRepositoriesAndConfigAroundTheRun(t *testing.T) {
	VerifyNoLeak(t, "TestGitIsolation_Probe")
}

func TestCeilingList_NamesEachDirOnceAndSkipsEmpty(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	sep := string(os.PathListSeparator)

	if got, want := ceilingList(a, "", b), a+sep+b; got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
	if got := ceilingList(""); got != "" {
		t.Errorf("ceilingList of nothing = %q, want empty", got)
	}
}

func TestCeilingList_AddsTheResolvedSpellingOfASymlinkedDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err) // skip-ok: an environment probe, symlinks need privilege on some platforms.
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}

	want := link + string(os.PathListSeparator) + resolved
	if got := ceilingList(link); got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
}

func TestEnclosingRepo_FindsTheNearestDirHoldingDotGit(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	deep := filepath.Join(inner, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// .git as a file, the way a linked worktree has it.
	if err := os.WriteFile(filepath.Join(inner, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := enclosingRepo(deep); got != inner {
		t.Errorf("enclosingRepo(%s) = %q, want %q", deep, got, inner)
	}
	if got := enclosingRepo(inner); got != inner {
		t.Errorf("a dir holding .git is its own repo: got %q, want %q", got, inner)
	}
	if got := enclosingRepo(outer); got != outer {
		t.Errorf("enclosingRepo(%s) = %q, want %q", outer, got, outer)
	}
}

func TestEnclosingRepo_AnswersEmptyForNoDir(t *testing.T) {
	if got := enclosingRepo(""); got != "" {
		t.Errorf("enclosingRepo of no dir = %q, want empty", got)
	}
}

// The walk ends at the filesystem root instead of turning on it.
func TestEnclosingRepo_StopsAtTheFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
		t.Skip("the filesystem root holds a .git") // skip-ok: an environment probe.
	}

	if got := enclosingRepo(root); got != "" {
		t.Errorf("enclosingRepo(%q) = %q, want empty", root, got)
	}
}
