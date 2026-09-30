package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// blockRepo commits a CLAUDE.md carrying the block this build renders for the
// repo as it stands.
func blockRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "# repo\n\n"+managedBlockFor(root))
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-q", "-m", "block")
	return root
}

// The escape: aphrollo.toml alone changed what the block renders, and nothing
// at commit time compared the committed block with the render.
func TestManagedBlockRefusal_RefusesAnAphrolloTomlChangeThatLeavesTheBlockStale(t *testing.T) {
	root := blockRepo(t)
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")
	gitDo(t, root, "add", "aphrollo.toml")

	got := ManagedBlockRefusal(root)
	for _, want := range []string{"CLAUDE.md", "aphrollo install --managed-block-only --repo ."} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal %q does not carry %q", got, want)
		}
	}
}

func TestManagedBlockRefusal_AdmitsTheCommitThatCarriesTheRefreshedBlock(t *testing.T) {
	root := blockRepo(t)
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "# repo\n\n"+managedBlockFor(root))
	gitDo(t, root, "add", "aphrollo.toml", "CLAUDE.md")

	if got := ManagedBlockRefusal(root); got != "" {
		t.Fatalf("refusal = %q, want none once the staged block equals the render", got)
	}
}

// Only the repo's own aphrollo.toml is a trigger, and only while staged: a
// stale block is another commit's business.
func TestManagedBlockRefusal_OnlyAStagedRootAphrolloTomlTriggers(t *testing.T) {
	root := blockRepo(t)
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")
	mustWrite(t, filepath.Join(root, "docs", "aphrollo.toml"), "[aphrollo]\nundercover = true\n")
	mustWrite(t, filepath.Join(root, "notes.md"), "x\n")
	gitDo(t, root, "add", "docs/aphrollo.toml", "notes.md")

	if got := ManagedBlockRefusal(root); got != "" {
		t.Fatalf("refusal = %q, want none with the root aphrollo.toml unstaged and a nested one staged", got)
	}
}

// A repo that keeps no CLAUDE.md, or one with no managed block, has no block to
// be behind.
func TestManagedBlockRefusal_AdmitsARepoWithNoManagedBlock(t *testing.T) {
	root := makeGoRepo(t)
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")
	gitDo(t, root, "add", "aphrollo.toml")
	if got := ManagedBlockRefusal(root); got != "" {
		t.Fatalf("no CLAUDE.md: refusal = %q, want none", got)
	}

	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "# repo, no block\n")
	gitDo(t, root, "add", "CLAUDE.md")
	if got := ManagedBlockRefusal(root); got != "" {
		t.Fatalf("CLAUDE.md with no block: refusal = %q, want none", got)
	}
}
