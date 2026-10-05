package escape

import (
	"path/filepath"
	"testing"
)

// A lane pruned between two sightings can no longer be asked for its git
// directory; its place under .worktrees/<repo>/ still names the repository.
func TestGitworldRepoKey_AGoneLaneKeysOnItsRepository(t *testing.T) {
	parent := t.TempDir()
	primary := filepath.Join(parent, "proj")
	gone := filepath.Join(parent, ".worktrees", "proj", "old-lane")
	other := filepath.Join(parent, ".worktrees", "proj", "another-lane")

	if a, b := gitworldRepoKey(gone), gitworldRepoKey(other); a != b {
		t.Errorf("two pruned lanes of one repository keyed %q and %q, want one key", a, b)
	}
	if a, b := gitworldRepoKey(gone), gitworldRepoKey(primary); a != b {
		t.Errorf("a pruned lane keyed %q, its repository %q, want the same", a, b)
	}
	if a, b := gitworldRepoKey(gone), gitworldRepoKey(filepath.Join(parent, ".worktrees", "elsewhere", "old-lane")); a == b {
		t.Errorf("lanes of two repositories share the key %q", a)
	}
}
