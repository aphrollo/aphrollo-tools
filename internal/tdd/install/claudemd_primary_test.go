package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The primary checkout of a repo with any linked worktree cannot commit at
// all (git shim WALL): it takes merges only. Writing the managed block there
// left the primary permanently dirty, which blocked workspace sync and made
// self-install build off a stale tree. WriteClaudeMD must leave that
// checkout's CLAUDE.md byte-identical and say why instead of dirtying it.

const oldManagedBlock = claudeMDBegin + "\nold operating instructions, superseded by the current template\n" + claudeMDEnd + "\n"

func TestWriteClaudeMD_LeavesAMergeOnlyPrimaryByteIdentical(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")
	addWorktree(t, root, "lane-a")

	path := filepath.Join(root, "CLAUDE.md")
	before := "# repo\n\n" + oldManagedBlock
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := WriteClaudeMD(root, t.TempDir(), false)
	if changed {
		t.Fatalf("changed = true, want false — a merge-only primary must not be written")
	}
	if !errors.Is(err, ErrManagedBlockInPrimary) {
		t.Fatalf("err = %v, want ErrManagedBlockInPrimary", err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != before {
		t.Fatalf("CLAUDE.md changed:\nbefore: %q\nafter:  %q", before, string(after))
	}
}

func TestWriteClaudeMD_StillWritesTheBlockInALaneOfThatRepo(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")
	lane := addWorktree(t, root, "lane-a")

	path := filepath.Join(lane, "CLAUDE.md")
	before := "# repo\n\n" + oldManagedBlock
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	changed, err := WriteClaudeMD(lane, shimDir, false)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true — a lane checkout must still get the block")
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	want := ClaudeMDBlock(shimDir, false, false)
	if !strings.Contains(string(after), want) {
		t.Fatalf("lane CLAUDE.md does not contain the current template verbatim:\n%s", after)
	}
}

// The sentinel means "a write would have changed this file and this is the
// merge-only primary" — not "this is the merge-only primary", full stop. A
// primary whose block is already current must be left alone in silence: no
// write, no error, nothing for gate init to relay as a lie about being
// behind the template.
func TestWriteClaudeMD_SaysNothingWhenThePrimaryBlockIsCurrent(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")
	addWorktree(t, root, "lane-a")

	shimDir := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")
	before := "# repo\n\n" + ClaudeMDBlock(shimDir, false, false)
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := WriteClaudeMD(root, shimDir, false)
	if err != nil {
		t.Fatalf("err = %v, want nil — the block is already current", err)
	}
	if changed {
		t.Fatalf("changed = true, want false — nothing needed writing")
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != before {
		t.Fatalf("CLAUDE.md changed:\nbefore: %q\nafter:  %q", before, string(after))
	}
}

// A merge-only primary that keeps no CLAUDE.md at all, asked without force,
// is the same no-op it is everywhere else — there is no write the primary
// check needs to veto.
func TestWriteClaudeMD_SaysNothingWhenThePrimaryHasNoClaudeMD(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")
	addWorktree(t, root, "lane-a")

	path := filepath.Join(root, "CLAUDE.md")
	changed, err := WriteClaudeMD(root, t.TempDir(), false)
	if err != nil {
		t.Fatalf("err = %v, want nil — no CLAUDE.md, force=false is a no-op", err)
	}
	if changed {
		t.Fatalf("changed = true, want false")
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatalf("CLAUDE.md was created: %v", serr)
	}
}

func TestWriteClaudeMD_StillWritesInASingleCheckoutClone(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")

	path := filepath.Join(root, "CLAUDE.md")
	before := "# repo\n\n" + oldManagedBlock
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := WriteClaudeMD(root, t.TempDir(), false)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true — a plain clone with no linked worktree is untouched by the rule")
	}
}

// addWorktree links a second checkout of root, which is what makes the
// primary one merge-only.
func addWorktree(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	gitDo(t, root, "worktree", "add", "-b", name, dir)
	return dir
}
