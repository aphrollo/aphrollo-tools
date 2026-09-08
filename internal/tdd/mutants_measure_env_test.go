package tdd

import (
	"testing"
)

// The marked run skips the build queue, so nothing else arbitrates who
// compiles into the directory it builds in — and a mutation build owns that
// directory for hours behind cargo's own blocking lock. Building where the
// lane builds would put an editor's slotted `cargo check` behind it with the
// queue none the wiser, so the run gets a target dir of its own, beside the
// temp dir it already keeps out of the way.
func TestMeasureEnv_BuildsInItsOwnTargetDirRatherThanTheLanes(t *testing.T) {
	root := t.TempDir()
	lane := ResolveCargoTargetDir(root)
	t.Setenv("CARGO_TARGET_DIR", lane)

	env := measureEnv(root, MutantsConfig{})

	// No CARGO_TARGET_DIR at all: every job builds in its own copy of the
	// tree. Inheriting the lane's would put the run behind the editor's
	// builds inside cargo's own lock, and one shared dir would put the
	// copies behind each other.
	if got := envValueOf(env, "CARGO_TARGET_DIR"); got != "" {
		t.Errorf("CARGO_TARGET_DIR = %q, want unset: each copy builds in its own target dir", got)
	}
}
