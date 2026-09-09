package workspace

import (
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// leaveTheBoxQueue marks every git this package's tests spawn — the
// fixtures' own setup and the verbs under test alike — as already queued, so
// the shim runs the real git straight through instead of taking the per-repo
// lock and waiting on the rest of the box. Set for the whole test binary,
// beside TestMain's other isolation from the operator's machine (HOME, gh),
// because that is what it is: the queue is the operator's, not this suite's.
//
// Safe HERE and nowhere near internal/cli: nothing in this package tests the
// shim's queuing. The shim's own tests live in internal/cli, which
// deliberately leaves this variable unset for the binary and opts a fixture
// out one command at a time (fixtureGitEnv).
func leaveTheBoxQueue() {
	if err := os.Setenv(tdd.GitQueuedEnv, "1"); err != nil {
		panic(err)
	}
}

// TestFixtureGit_RunsOutsideTheBoxQueue pins what this package's fixtures
// need from `git`: git itself, not the gate around it.
//
// On an operator box `git` resolves to the aphrollo queue shim, which
// serialises a mutating git behind whatever else the box is doing. That is
// exactly right for a session's own `git commit` and wrong for a fixture:
// this package builds dozens of throwaway repos per run, and two runs of it
// died at 25m and 60m stuck in repoWithOrigin's git calls waiting on other
// sessions' work — against 117s for the same package with the shim off PATH.
// The shim already honours a marker for "this git is nested inside something
// that queued already, run straight through"; the fixtures set it, so a
// fixture repo's setup never enters the queue.
//
// The assertion is on the ENVIRONMENT a fixture's git child actually gets,
// never on how long the package takes: a wall-clock assertion measures the
// box, not this change.
func TestFixtureGit_RunsOutsideTheBoxQueue(t *testing.T) {
	cmd := exec.Command("git", "-C", t.TempDir(), "commit", "-qm", "fixture")

	if !slices.Contains(cmd.Environ(), tdd.GitQueuedEnv+"=1") {
		t.Fatalf("a fixture git runs without %s=1, so every mutating fixture command queues behind the whole box", tdd.GitQueuedEnv)
	}
}
