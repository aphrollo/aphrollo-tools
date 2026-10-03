package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world. See gitiso.Isolate.
func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}

// gitT runs real git in dir for a test's setup and answers its trimmed stdout.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repoWithCommit is a repository on branch main with one commit.
func repoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "one")
	return dir
}

// sameDir reports whether two spellings name the one directory: a temp dir
// may come back through a short name or a symlink.
func sameDir(t *testing.T, got, want string) bool {
	t.Helper()
	g, err := os.Stat(got)
	if err != nil {
		return false
	}
	w, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(g, w)
}

func mustNew(t *testing.T, dir string) *Client {
	t.Helper()
	c, err := New(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNew_FindsTheRootAndBothGitDirsWithoutSpawning(t *testing.T) {
	main := repoWithCommit(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitT(t, main, "worktree", "add", "-q", "-b", "lane/x", lane)
	write(t, lane, "deep/er/f.txt", "x\n")

	tests := []struct {
		name, start, root, gitDir string
		linked                    bool
	}{
		{"main checkout", main, main, filepath.Join(main, ".git"), false},
		{"directory below the root", filepath.Join(lane, "deep", "er"), lane, filepath.Join(main, ".git", "worktrees", "lane"), true},
		{"linked worktree", lane, lane, filepath.Join(main, ".git", "worktrees", "lane"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mustNew(t, tc.start)
			if !sameDir(t, c.Root(), tc.root) {
				t.Errorf("Root = %q, want %q", c.Root(), tc.root)
			}
			if !sameDir(t, c.GitDir(), tc.gitDir) {
				t.Errorf("GitDir = %q, want %q", c.GitDir(), tc.gitDir)
			}
			if !sameDir(t, c.CommonDir(), filepath.Join(main, ".git")) {
				t.Errorf("CommonDir = %q, want %q", c.CommonDir(), filepath.Join(main, ".git"))
			}
			if c.IsLinkedWorktree() != tc.linked {
				t.Errorf("IsLinkedWorktree = %v, want %v", c.IsLinkedWorktree(), tc.linked)
			}
			if c.Spawns() != 0 {
				t.Errorf("%d spawns to find what the files hold", c.Spawns())
			}
		})
	}
}

func TestNew_RefusesADirectoryInNoRepository(t *testing.T) {
	_, err := New(t.TempDir(), Options{})
	if !errors.Is(err, ErrNotRepo) {
		t.Errorf("New outside a repository = %v, want ErrNotRepo", err)
	}
}

func TestHead_ReadsBranchAndCommitFromTheFiles(t *testing.T) {
	dir := repoWithCommit(t)
	sha := gitT(t, dir, "rev-parse", "HEAD")
	c := mustNew(t, dir)

	h, err := c.Head()
	if err != nil {
		t.Fatal(err)
	}
	if want := (Head{Ref: "refs/heads/main", Branch: "main", SHA: sha}); h != want {
		t.Errorf("Head = %+v, want %+v", h, want)
	}

	// After packing, the commit is in packed-refs and no loose file is left.
	gitT(t, dir, "pack-refs", "--all", "--prune")
	if h, _ = c.Head(); h.SHA != sha {
		t.Errorf("packed Head.SHA = %q, want %q", h.SHA, sha)
	}

	gitT(t, dir, "checkout", "-q", "--detach")
	if h, _ = c.Head(); h != (Head{SHA: sha, Detached: true}) {
		t.Errorf("detached Head = %+v, want the commit and no branch", h)
	}
	if c.Spawns() != 0 {
		t.Errorf("%d spawns reading HEAD", c.Spawns())
	}
}

func TestHead_AnUnbornBranchHasANameAndNoCommit(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "fresh")
	h, err := mustNew(t, dir).Head()
	if err != nil {
		t.Fatal(err)
	}
	if want := (Head{Ref: "refs/heads/fresh", Branch: "fresh"}); h != want {
		t.Errorf("Head = %+v, want %+v", h, want)
	}
}

func TestHead_AReftableRepositoryIsAskedOfGitNotReadFromFiles(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main", "--ref-format=reftable")
	write(t, dir, "a.txt", "a\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "one")
	c := mustNew(t, dir)
	h, err := c.Head()
	if err != nil {
		t.Fatal(err)
	}
	if want := (Head{Ref: "refs/heads/main", Branch: "main", SHA: gitT(t, dir, "rev-parse", "HEAD")}); h != want {
		t.Errorf("Head = %+v, want %+v", h, want)
	}
	if c.Spawns() == 0 {
		t.Error("a reftable repository was read as files")
	}
}

func TestWorktrees_ListsTheMainCheckoutAndEveryLinkedOne(t *testing.T) {
	main := repoWithCommit(t)
	laneA := filepath.Join(t.TempDir(), "a")
	laneB := filepath.Join(t.TempDir(), "b")
	gitT(t, main, "worktree", "add", "-q", "-b", "lane/a", laneA)
	gitT(t, main, "worktree", "add", "-q", "--detach", laneB)
	sha := gitT(t, main, "rev-parse", "HEAD")

	// Asked from a linked worktree, the answer is the same list.
	for _, from := range []string{main, laneA} {
		c := mustNew(t, from)
		got, err := c.Worktrees()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("from %s: %d worktrees, want 3: %+v", from, len(got), got)
		}
		want := []Worktree{
			{Path: main, Head: Head{Ref: "refs/heads/main", Branch: "main", SHA: sha}},
			{Path: laneA, Head: Head{Ref: "refs/heads/lane/a", Branch: "lane/a", SHA: sha}},
			{Path: laneB, Head: Head{SHA: sha, Detached: true}},
		}
		for _, w := range want {
			found := false
			for _, g := range got {
				if sameDir(t, g.Path, w.Path) && g.Head == w.Head {
					found = true
				}
			}
			if !found {
				t.Errorf("from %s: %+v missing from %+v", from, w, got)
			}
		}
		if c.Spawns() != 0 {
			t.Errorf("%d spawns listing worktrees", c.Spawns())
		}
	}
}

func TestMergeInProgress_NamesTheRefAnUnfinishedMergeLeft(t *testing.T) {
	dir := repoWithCommit(t)
	c := mustNew(t, dir)
	if got := c.MergeInProgress(); got != "" {
		t.Fatalf("a quiet repository reports %q", got)
	}
	gitT(t, dir, "checkout", "-q", "-b", "side")
	write(t, dir, "a.txt", "side\n")
	gitT(t, dir, "commit", "-q", "-am", "side")
	gitT(t, dir, "checkout", "-q", "main")
	write(t, dir, "a.txt", "main\n")
	gitT(t, dir, "commit", "-q", "-am", "main")
	out, err := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "merge", "side").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "CONFLICT") {
		t.Fatalf("the merge did not stop on a conflict: %v: %s", err, out)
	}
	if got := c.MergeInProgress(); got != "MERGE_HEAD" {
		t.Errorf("MergeInProgress = %q, want MERGE_HEAD", got)
	}
	if c.Spawns() != 0 {
		t.Errorf("%d spawns checking for a merge", c.Spawns())
	}
}

func TestMergeInProgress_AFileHoldingNoCommitIsNoMerge(t *testing.T) {
	dir := repoWithCommit(t)
	write(t, dir, ".git/MERGE_HEAD", "not a commit\n")
	if got := mustNew(t, dir).MergeInProgress(); got != "" {
		t.Errorf("MergeInProgress = %q, want none: git does not resolve that file either", got)
	}
}

func TestOutput_RunsGitInTheClientsRepositoryWhateverTheHookExported(t *testing.T) {
	mine := repoWithCommit(t)
	other := repoWithCommit(t)
	// A git hook exports GIT_DIR for the repository it runs for.
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	c := mustNew(t, mine)
	got, err := c.Output("rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(t, strings.TrimSpace(got), mine) {
		t.Errorf("rev-parse --show-toplevel = %q, want %q: GIT_DIR redirected the call", got, mine)
	}
}

func TestOutput_CountsAFailedSpawnAndKeepsTheStderr(t *testing.T) {
	c := mustNew(t, repoWithCommit(t))
	_, err := c.Output("rev-parse", "--verify", "no-such-ref-anywhere")
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want an *exec.ExitError", err)
	}
	if len(exit.Stderr) == 0 {
		t.Error("git's stderr is not on the error")
	}
	if c.Spawns() != 1 {
		t.Errorf("Spawns = %d after one failed call, want 1", c.Spawns())
	}
}

func TestStatus_OneSpawnAnswersEveryCallOfTheSameBatch(t *testing.T) {
	dir := repoWithCommit(t)
	write(t, dir, "a.txt", "changed\n")
	write(t, dir, "new/inside.txt", "n\n")
	write(t, dir, "staged.txt", "s\n")
	gitT(t, dir, "add", "staged.txt")
	c := mustNew(t, dir)

	st, err := c.Status("batch-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.StagedPaths(); !reflect.DeepEqual(got, []string{"staged.txt"}) {
		t.Errorf("staged = %q", got)
	}
	if got := st.UnstagedPaths(); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Errorf("unstaged = %q", got)
	}
	if got := st.UntrackedPaths(); !reflect.DeepEqual(got, []string{"new/inside.txt"}) {
		t.Errorf("untracked = %q: a new directory must list its files, not itself", got)
	}
	if st.Branch.Head != "main" {
		t.Errorf("Branch.Head = %q, want main", st.Branch.Head)
	}

	again, err := c.Status("batch-1")
	if err != nil || again != st {
		t.Errorf("the same batch got a new answer: %v", err)
	}
	if c.Spawns() != 1 {
		t.Fatalf("Spawns = %d after two calls of one batch, want 1", c.Spawns())
	}

	// A new batch reads the tree as it is now.
	gitT(t, dir, "add", "a.txt")
	next, err := c.Status("batch-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := next.StagedPaths(); !reflect.DeepEqual(got, []string{"a.txt", "staged.txt"}) {
		t.Errorf("staged in batch-2 = %q", got)
	}
	if c.Spawns() != 2 {
		t.Errorf("Spawns = %d after a second batch, want 2", c.Spawns())
	}
}

func TestStatus_AnEmptyBatchKeyIsNeverCached(t *testing.T) {
	c := mustNew(t, repoWithCommit(t))
	for range 2 {
		if _, err := c.Status(""); err != nil {
			t.Fatal(err)
		}
	}
	if c.Spawns() != 2 {
		t.Errorf("Spawns = %d for two keyless calls, want 2", c.Spawns())
	}
}

func TestStatus_AFailedCallIsTheBatchsAnswerAndANewBatchTriesAgain(t *testing.T) {
	dir := repoWithCommit(t)
	c := mustNew(t, dir)
	// An index git cannot read makes status fail.
	idx := filepath.Join(dir, ".git", "index")
	good, err := os.ReadFile(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idx, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := c.Status("b"); err == nil {
			t.Fatal("status of a corrupt index succeeded")
		}
	}
	if c.Spawns() != 1 {
		t.Errorf("a batch of three questions of an unreadable repository spawned git %d times, want 1", c.Spawns())
	}
	if err := os.WriteFile(idx, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status("b2"); err != nil {
		t.Errorf("the repaired repository still fails in a new batch: %v", err)
	}
}

func TestStatus_AheadAndBehindComeFromTheUpstreamTheCloneTracks(t *testing.T) {
	origin := repoWithCommit(t)
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, origin, "clone", "-q", origin, clone)
	write(t, clone, "local.txt", "l\n")
	gitT(t, clone, "add", "-A")
	gitT(t, clone, "commit", "-q", "-m", "local")
	st, err := mustNew(t, clone).Status("b")
	if err != nil {
		t.Fatal(err)
	}
	b := st.Branch
	if b.Head != "main" || b.Upstream != "origin/main" || b.Ahead != 1 || b.Behind != 0 || !b.Tracked {
		t.Errorf("Branch = %+v, want main tracking origin/main, 1 ahead 0 behind", b)
	}
}

func TestIsObjectID_AcceptsOnlyAFullLowercaseHexName(t *testing.T) {
	sha1 := strings.Repeat("0123456789abcdef", 3)[:40]
	tests := []struct {
		name, id string
		want     bool
	}{
		{"sha-1", sha1, true},
		{"sha-256", sha1 + strings.Repeat("9f", 12), true},
		{"first and last digits", strings.Repeat("0", 20) + strings.Repeat("f", 20), true},
		{"one short", sha1[:39], false},
		{"one long", sha1 + "a", false},
		{"between sha-1 and sha-256", sha1 + "ab", false},
		{"one past f", sha1[:39] + "g", false},
		{"one before a", sha1[:39] + "`", false},
		{"one past 9", sha1[:39] + ":", false},
		{"one before 0", sha1[:39] + "/", false},
		{"not a commit at all", "not a commit", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		if got := isObjectID(tc.id); got != tc.want {
			t.Errorf("%s: isObjectID(%q) = %v, want %v", tc.name, tc.id, got, tc.want)
		}
	}
}

func TestStatus_AKeylessReadLeavesTheKeptBatchAnswerAlone(t *testing.T) {
	dir := repoWithCommit(t)
	c := mustNew(t, dir)
	kept, err := c.Status("batch")
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "later.txt", "l\n")

	fresh, err := c.Status("")
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Status("batch")

	if got := fresh.UntrackedPaths(); !reflect.DeepEqual(got, []string{"later.txt"}) {
		t.Errorf("the keyless read saw %q, want the file written since", got)
	}
	if err != nil || again != kept {
		t.Errorf("the batch answer after a keyless read = %p, %v; want the one kept, %p", again, err, kept)
	}
	if c.Spawns() != 2 {
		t.Errorf("Spawns = %d, want 2: the batch's read and the keyless one, none to answer the batch again", c.Spawns())
	}
}

// ratchet: test_removed TestStatus_AFailedCallIsNotCachedAsTheBatchsAnswer: a failed status is now the batch's answer; TestStatus_AFailedCallIsTheBatchsAnswerAndANewBatchTriesAgain pins it
