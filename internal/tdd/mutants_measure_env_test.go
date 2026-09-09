package tdd

import (
	"testing"
)

// The marked run skips the build queue, so nothing else arbitrates who
// compiles into the directory it builds in — and a mutation build owns that
// directory for hours behind cargo's own blocking lock. Building where the
// lane builds would put an editor's slotted `cargo check` behind it with the
// queue none the wiser, so each shard gets a target dir of its own, beside
// the temp dir it already keeps out of the way. Its own, not merely not the
// lane's: one directory shared by N shards would put them behind that same
// build-directory lock one after another, which is the serial run the shards
// exist to end.
func TestMeasureEnv_BuildsInItsOwnTargetDirRatherThanTheLanes(t *testing.T) {
	root := t.TempDir()
	lane := ResolveCargoTargetDir(root)
	t.Setenv("CARGO_TARGET_DIR", lane)

	// The shared half names no build directory at all: there is no one
	// directory a sharded run builds in.
	if got := envValueOf(measureEnv(root, MutantsConfig{}), "CARGO_TARGET_DIR"); got != "" {
		t.Errorf("CARGO_TARGET_DIR = %q, want unset: the shard decides where it builds", got)
	}

	seen := map[string]bool{}
	for shard := range 2 {
		env := measureShardEnv(root, MutantsConfig{}, shard, 2, false)
		got := envValueOf(env, "CARGO_TARGET_DIR")
		if want := mutantsShardTargetDir(root, shard); got != want {
			t.Errorf("shard %d builds in %q, want its own persistent %q", shard, got, want)
		}
		if got == lane {
			t.Errorf("shard %d builds in the LANE's target dir %q — an editor's slotted build waits hours on it", shard, lane)
		}
		if seen[got] {
			t.Errorf("shard %d shares its target dir %q with another shard: cargo serialises them on its "+
				"build-directory lock, which is the one-at-a-time run the shards exist to end", shard, got)
		}
		seen[got] = true
	}
}
