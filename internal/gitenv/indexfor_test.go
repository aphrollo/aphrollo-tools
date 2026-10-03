package gitenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(Clean(), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, nil, "init", "-q")
	gitIn(t, dir, nil, "config", "user.email", "t@example.invalid")
	gitIn(t, dir, nil, "config", "user.name", "t")
	return dir
}

func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// `git commit -a` runs the hook with GIT_INDEX_FILE naming the index it is
// building. A git call the hook makes for that repository must read it, or it
// reads the stale default index; a call for any other repository must not.
func TestCleanFor_CarriesTheHooksIndexOnlyToTheRepositoryItBelongsTo(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, nil, "add", "f.txt")
	gitIn(t, repo, nil, "commit", "-q", "-m", "first")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The index `git commit -a` would build: v2 staged, the default index left at v1.
	temp := filepath.Join(repo, ".git", "next-index-test.lock")
	gitIn(t, repo, []string{"GIT_INDEX_FILE=" + temp}, "read-tree", "HEAD")
	gitIn(t, repo, []string{"GIT_INDEX_FILE=" + temp}, "add", "f.txt")
	t.Setenv("GIT_INDEX_FILE", temp)

	got, ok := envValue(CleanFor(repo, "show", ":f.txt"), "GIT_INDEX_FILE")
	if !ok || got != temp {
		t.Fatalf("GIT_INDEX_FILE for the hook's own repository = %q (%v), want %q", got, ok, temp)
	}
	cmd := exec.Command("git", "-C", repo, "show", ":f.txt")
	cmd.Env = CleanFor(repo, "show", ":f.txt")
	out, err := cmd.Output()
	if err != nil || string(out) != "v2\n" {
		t.Errorf("git show :f.txt under CleanFor = %q (%v), want the staged v2", out, err)
	}

	other := initRepo(t)
	if v, ok := envValue(CleanFor(other, "show", ":f.txt"), "GIT_INDEX_FILE"); ok {
		t.Errorf("another repository was handed the hook's index %q", v)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	gitIn(t, repo, nil, "worktree", "add", "-q", "--detach", linked, "HEAD")
	if v, ok := envValue(CleanFor(linked, "show", ":f.txt"), "GIT_INDEX_FILE"); ok {
		t.Errorf("a linked worktree of that repository, whose index is its own, was handed %q", v)
	}
	if _, ok := envValue(CleanFor(filepath.Join(repo, "no", "such", "dir"), "show", ":f.txt"), "GIT_INDEX_FILE"); !ok {
		t.Error("a directory that does not exist yet inside the repository lost the index")
	}
}

func TestCleanFor_ReadsARelativeIndexAgainstTheHooksWorkingDirectory(t *testing.T) {
	repo := initRepo(t)
	t.Chdir(repo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(".git", "index.lock"))
	got, ok := envValue(CleanFor(repo, "show", ":f.txt"), "GIT_INDEX_FILE")
	want, _ := filepath.Abs(filepath.Join(".git", "index.lock"))
	if !ok || got != want {
		t.Fatalf("GIT_INDEX_FILE = %q (%v), want the absolute %q", got, ok, want)
	}
}

func TestCleanFor_AddsNoIndexWhenTheProcessHasNone(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("GIT_INDEX_FILE", "")
	if v, ok := envValue(CleanFor(repo, "show", ":f.txt"), "GIT_INDEX_FILE"); ok && v != "" {
		t.Errorf("GIT_INDEX_FILE = %q with none in the environment", v)
	}
}

// The hook's index belongs to the commit being built. A call that reads the
// staged tree must see it; a call that writes an index or checks a tree out
// would write through it instead of into the index it means (a `worktree add`
// resets it to HEAD, and the commit lands empty), so it gets none.
func TestCleanFor_KeepsTheHooksIndexOnlyForCallsThatReadTheStagedTree(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(repo, ".git", "index"))
	cases := []struct {
		args []string
		keep bool
	}{
		{[]string{"ls-files", "--cached"}, true},
		{[]string{"diff", "--cached", "--name-only"}, true},
		{[]string{"diff", "--name-only"}, true},
		{[]string{"diff-index", "--cached", "HEAD"}, true},
		{[]string{"show", ":f.txt"}, true},
		{[]string{"cat-file", "--batch"}, true},
		{[]string{"write-tree"}, true},
		{[]string{"-C", "elsewhere", "-c", "core.quotepath=off", "--no-pager", "ls-files"}, true},
		{[]string{"worktree", "add", "--detach", "wt", "HEAD"}, false},
		{[]string{"-C", "elsewhere", "worktree", "add", "wt"}, false},
		{[]string{"checkout", "HEAD", "--", "f.txt"}, false},
		{[]string{"read-tree", "-u", "--reset", "HEAD"}, false},
		{[]string{"reset", "--hard"}, false},
		{[]string{"stash", "push"}, false},
		{[]string{"update-index", "--refresh"}, false},
		{[]string{"apply", "--cached", "p.diff"}, false},
		{[]string{"commit", "-m", "x"}, false},
		{[]string{"-C", "elsewhere"}, false},
		{nil, false},
	}
	for _, c := range cases {
		_, got := envValue(CleanFor(repo, c.args...), "GIT_INDEX_FILE")
		if got != c.keep {
			t.Errorf("git %s: GIT_INDEX_FILE carried = %v, want %v", strings.Join(c.args, " "), got, c.keep)
		}
	}
}
