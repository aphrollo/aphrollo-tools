package cli

import (
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// fixtureGitEnv is the environment a git spawned by a TEST FIXTURE runs
// with: this process's own, plus the marker the queue shim reads as "the
// caller already queued, run the real git straight through". A fixture wants
// git; the queue in front of it belongs to the operator's box, and a
// throwaway repo's `init`/`add`/`commit` waiting on somebody else's build
// slot is how two runs of internal/workspace died at 25m and 60m (117s with
// the shim off PATH).
//
// Per COMMAND here, never for the test binary: runGitShim's first decision is
// whether it is nested inside a queued git, so a package-wide marker would
// send every shim test down the passthrough branch and stop it exercising the
// queuing, refusals and locking it exists to pin — see
// TestPackageEnv_LeavesTheQueueMarkerUnsetForTheShimsOwnTests. A git this
// package runs AS THE SUBJECT of a test keeps its own environment.
func fixtureGitEnv() []string {
	return append(os.Environ(), tdd.GitQueuedEnv+"=1")
}

// fixtureGit is exec.Command("git", …) for a fixture: the same command, built
// with fixtureGitEnv. A drop-in at every call site, which is the point — a
// fixture helper keeps its shape (and its file its length) instead of growing
// an env line each, and the next one is written by reaching for this name.
func fixtureGit(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Env = fixtureGitEnv()
	return cmd
}

// TestFixtureGitEnv_CarriesTheQueueBypassMarker pins what a FIXTURE git in
// this package runs with. Same defect as internal/workspace's
// TestFixtureGit_RunsOutsideTheBoxQueue: `git` on an operator box is the
// aphrollo queue shim, and a fixture that builds a throwaway repo does not
// want the box's queue in front of its `init`/`add`/`commit` — it wants git.
// fixtureGitEnv is the one environment those calls run with, so the marker is
// asserted there rather than at twelve call sites.
func TestFixtureGitEnv_CarriesTheQueueBypassMarker(t *testing.T) {
	if !slices.Contains(fixtureGitEnv(), tdd.GitQueuedEnv+"=1") {
		t.Fatalf("fixtureGitEnv() omits %s=1, so every mutating fixture command queues behind the whole box", tdd.GitQueuedEnv)
	}
}

// TestPackageEnv_LeavesTheQueueMarkerUnsetForTheShimsOwnTests is the other
// half of the same rule, and the reason this package cannot take
// internal/workspace's package-wide fix. runGitShim's FIRST decision is
// whether it is already nested inside a queued git; with the marker set for
// the whole test binary every shim test would take that passthrough branch
// and stop exercising the queuing, the refusals and the locking they exist
// to pin. A fixture opts out per command; the package never does.
func TestPackageEnv_LeavesTheQueueMarkerUnsetForTheShimsOwnTests(t *testing.T) {
	if inheritedQueueMarker == "1" {
		// The gate's own deferred phase runs `go test` as a child of a git it
		// has already queued, and passes the marker down. The environment came
		// from OUTSIDE this binary, so what this test pins — that the package
		// never sets it — cannot be observed here, and failing would report a
		// pipeline condition as a defect in the package. The shim tests that
		// depend on the marker being unset are the ones that would then fail,
		// loudly and on their own terms.
		t.Skipf("%s=1 was inherited from the environment this binary was started in", tdd.GitQueuedEnv)
	}
	if os.Getenv(tdd.GitQueuedEnv) == "1" {
		t.Fatalf("%s=1 is set for the whole test binary — the shim's own queuing tests would pass through instead of queuing", tdd.GitQueuedEnv)
	}
}

// inheritedQueueMarker is the marker's value as this test BINARY was started,
// read before any test can set it. It is what separates "the package set it"
// from "the process that launched us had it set".
var inheritedQueueMarker = os.Getenv(tdd.GitQueuedEnv)
