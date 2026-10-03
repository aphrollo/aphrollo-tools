package gitx

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sameWorktreeDir(t *testing.T, got, want string) bool {
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

func TestHookClient_IsOneClientForEveryDirectoryOfAWorktree(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "deep/er/f.go", "package m\n")

	top, below := HookClient(root), HookClient(filepath.Join(root, "deep", "er"))

	if top == nil || top != below {
		t.Fatalf("clients = %p and %p, want the one client of the worktree", top, below)
	}
	if got := HookClient(t.TempDir()); got != nil {
		t.Errorf("a directory in no repository got a client: %v", got.Root())
	}
	if got := HookClient(""); got != nil {
		t.Errorf("no directory got a client: %v", got.Root())
	}
}

func TestHookStatus_ReadsTheTreeOncePerBatch(t *testing.T) {
	root := makeGoRepo(t)
	BeginHook()
	c, first := HookStatus(root)
	if first == nil {
		t.Fatal("no status for a repository")
	}
	write(t, root, "later.go", "package m\n")

	_, same := HookStatus(root)
	if same != first || c.Spawns() != 1 {
		t.Errorf("the same batch read the tree again: same answer %v, %d spawns, want the one answer and 1 spawn", same == first, c.Spawns())
	}

	BeginHook()
	_, next := HookStatus(root)
	if got := next.UntrackedPaths(); !reflect.DeepEqual(got, []string{"later.go"}) {
		t.Errorf("a new batch saw %q, want the file written since", got)
	}
	if c.Spawns() != 2 {
		t.Errorf("%d spawns after two batches, want 2", c.Spawns())
	}
}

func TestFreshStatus_ReadsTheTreeAgainAndLeavesTheBatchAlone(t *testing.T) {
	root := makeGoRepo(t)
	BeginHook()
	c, batch := HookStatus(root)
	write(t, root, "later.go", "package m\n")

	_, fresh := FreshStatus(root)
	_, again := HookStatus(root)

	if got := fresh.UntrackedPaths(); !reflect.DeepEqual(got, []string{"later.go"}) {
		t.Errorf("the fresh read saw %q, want the file written since", got)
	}
	if again != batch || c.Spawns() != 2 {
		t.Errorf("the batch's answer after a fresh read: same %v, %d spawns; want the kept one and 2 spawns", again == batch, c.Spawns())
	}
}

func TestHookStatus_IsNilOutsideARepository(t *testing.T) {
	if c, st := HookStatus(t.TempDir()); c != nil || st != nil {
		t.Errorf("HookStatus outside a repository = %v, %v; want nil, nil", c, st)
	}
	if c, st := FreshStatus(t.TempDir()); c != nil || st != nil {
		t.Errorf("FreshStatus outside a repository = %v, %v; want nil, nil", c, st)
	}
}

func TestHookRoot_NamesTheWorktreeTopFromAnyDirectoryBelowItAsGitDoes(t *testing.T) {
	main := makeGoRepo(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, main, "worktree", "add", "-q", "-b", "lane/x", lane)
	write(t, lane, "deep/er/f.go", "package m\n")

	for _, dir := range []string{main, lane, filepath.Join(lane, "deep", "er")} {
		want := RepoRoot(dir)
		if got := HookRoot(dir); !sameWorktreeDir(t, got, want) {
			t.Errorf("HookRoot(%s) = %q, want %q, the top git names", dir, got, want)
		}
	}
	if got := HookRoot(t.TempDir()); got != "" {
		t.Errorf("HookRoot of a directory in no repository = %q, want none", got)
	}
}

func TestHeadCopy_ReadsWhatHeadHoldsForADirtyFileWithoutAskingGitForIt(t *testing.T) {
	root := makeGoRepo(t)
	committed, err := os.ReadFile(filepath.Join(root, "doc.go"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "doc.go", "package m\n\nfunc Changed() {}\n")
	write(t, root, "fresh.go", "package m\n")
	write(t, root, "staged.go", "package m\n")
	gitDo(t, root, "add", "staged.go")
	BeginHook()
	c, _ := HookStatus(root)
	spawned := c.Spawns()

	tests := []struct {
		name, file, text string
		inHead, ok       bool
	}{
		{"a modified tracked file", "doc.go", string(committed), true, true},
		{"an untracked file", "fresh.go", "", false, true},
		{"a newly added file", "staged.go", "", false, true},
		{"a clean tracked file, which only git can tell from an ignored one", "go.mod", "", false, false},
	}
	for _, tc := range tests {
		text, inHead, ok := HeadCopy(filepath.Join(root, tc.file))
		if text != tc.text || inHead != tc.inHead || ok != tc.ok {
			t.Errorf("%s: HeadCopy = %q, %v, %v; want %q, %v, %v", tc.name, text, inHead, ok, tc.text, tc.inHead, tc.ok)
		}
	}
	if c.Spawns() != spawned {
		t.Errorf("reading the committed copies spawned git %d more times, want none", c.Spawns()-spawned)
	}
}

func TestHeadCopy_SaysItCannotTellOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "f.go", "package m\n")

	if text, inHead, ok := HeadCopy(filepath.Join(dir, "f.go")); text != "" || inHead || ok {
		t.Errorf("HeadCopy outside a repository = %q, %v, %v; want it unable to tell", text, inHead, ok)
	}
}

func TestHeadCopy_AMovedFileHasNoCopyUnderItsNewName(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "mv", "doc.go", "moved.go")
	BeginHook()

	text, inHead, ok := HeadCopy(filepath.Join(root, "moved.go"))

	if text != "" || inHead || !ok {
		t.Errorf("HeadCopy of a rename's destination = %q, %v, %v; want no copy, known", text, inHead, ok)
	}
}
