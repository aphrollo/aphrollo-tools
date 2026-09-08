package tdd

import (
	"path/filepath"
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

	want := filepath.Join(lane, "mutants", "target")
	got := envValueOf(env, "CARGO_TARGET_DIR")
	if got != want {
		t.Errorf("CARGO_TARGET_DIR = %q, want %q", got, want)
	}
	if got == lane {
		t.Error("the mutation run builds where the lane builds — an editor's build would queue behind it inside cargo's own lock, invisible to the queue")
	}
}
