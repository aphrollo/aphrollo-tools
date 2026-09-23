package gitx

import (
	"os/exec"
	"slices"
	"testing"
)

// TestFixtureGit_RunsOutsideTheBoxQueue is internal/workspace's
// TestFixtureGit_RunsOutsideTheBoxQueue for this package: the same box-wide
// `git` shim brokers the same fixture repos here, and a fixture wants git,
// not the queue in front of it. This package's PRODUCTION git calls already
// carry the marker (cleanGitEnv); only the test fixtures' own bare
// exec.Command("git", ...) calls were still entering the queue.
func TestFixtureGit_RunsOutsideTheBoxQueue(t *testing.T) {
	cmd := exec.Command("git", "-C", t.TempDir(), "commit", "-qm", "fixture")

	if !slices.Contains(cmd.Environ(), GitQueuedEnv+"=1") {
		t.Fatalf("a fixture git runs without %s=1, so every mutating fixture command queues behind the whole box", GitQueuedEnv)
	}
}
