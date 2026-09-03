package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// cargo-mutants is the one cargo subcommand that can hold the box hostage.
// Run bare it copies the whole tree into the OS temp dir and builds it cold,
// once per job, for hours — measured: two runs, post-edit hooks waiting up to
// 619 s, 56 deferred in three hours, 11 tree copies left behind at ~135 MB
// each. Run through the gate's own job it mutates a dedicated warm worktree in
// place and touches nobody else's target dir at all.
//
// So the shim takes two positions on it, and they are the same position:
// the bare invocation is REFUSED, and the gated one is let PAST the queue —
// because the thing that made queueing necessary (sharing a target dir) is
// exactly what the gated run does not do.

// mutantsRefusal is the whole rejection: what to run instead, and why the
// invocation typed was not it.
const mutantsRefusal = "gate: run tools/mutation_gate.sh <base> — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours"

// deprecatedMutationGateEnv is the one-release grace: the old handshake
// variable no longer decides anything (a nested `cargo mutants` is let
// through by its target dir, same as everything else), but a caller still
// setting it is not refused outright — it is let through once more and
// logged, so the removal shows up before it breaks anyone.
const deprecatedMutationGateEnv = "APHROLLO_MUTATION_GATE"

// refuseBareMutants reports whether this invocation is a hand-typed `cargo
// mutants`, and prints the one line that says what to do instead. targetDir
// is the shim's own resolution of where THIS invocation would build: the
// gated run mutates its dedicated worktree in place, so a call building into
// that worktree's own target dir is the gated run, whatever environment it
// carries or does not. The old MUTATION_GATE handshake variable is dead —
// this no longer reads it at all.
func refuseBareMutants(args []string, targetDir string, stderr io.Writer) bool {
	if cargoVerb(args) != "mutants" || underMutantsWorktree(targetDir) {
		return false
	}
	if os.Getenv(deprecatedMutationGateEnv) == "1" {
		logDeprecatedMutationGateEnv(targetDir)
		return false
	}
	fmt.Fprintln(stderr, mutantsRefusal)
	return true
}

// deprecatedMutationGateLogged makes the deprecation notice fire once per
// process, matching bypassLogged: a build invokes the shim many times, and
// one line per invocation would drown the log it is meant to make readable.
var deprecatedMutationGateLogged sync.Once

// resetDeprecatedMutationGateLog lets a test observe the once-per-process rule.
func resetDeprecatedMutationGateLog() { deprecatedMutationGateLogged = sync.Once{} }

// logDeprecatedMutationGateEnv records a call that relied on the retired
// handshake variable rather than on building into the mutants worktree.
func logDeprecatedMutationGateEnv(targetDir string) {
	deprecatedMutationGateLogged.Do(func() {
		tdd.AppendGateLog("precommit", targetDir, "cargo", "mutation-gate-env-deprecated", 0)
	})
}

// queueBypassAllowed reports whether this invocation may skip the build queue
// entirely. Both halves are required: the environment must ASK for it (the
// mutation job sets it), and the target dir being built into must be the
// dedicated mutants worktree's own. The second half is what keeps the switch
// from being a general-purpose "ignore the queue" — an ordinary build that
// bypassed would compile into a directory another build owns.
func queueBypassAllowed(targetDir string) bool {
	if os.Getenv(tdd.QueueEnv) != tdd.QueueBypass || targetDir == "" {
		return false
	}
	return underMutantsWorktree(targetDir)
}

// underMutantsWorktree reports whether dir is inside a
// <parent>/.worktrees/<repo>/mutants tree — the layout MutantsWorktreeDir
// creates, recognised by shape rather than by a path handed over in an
// environment variable, which anything could set.
func underMutantsWorktree(dir string) bool {
	dir = filepath.Clean(dir)
	for {
		if filepath.Base(dir) == "mutants" && filepath.Base(filepath.Dir(filepath.Dir(dir))) == ".worktrees" {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir || strings.TrimSpace(parent) == "" {
			return false
		}
		dir = parent
	}
}

// bypassLogged makes the bypass record itself exactly once per process: a
// build invokes the shim many times, and one line per invocation would drown
// the log it is meant to make readable.
var bypassLogged sync.Once

// resetBypassLog lets a test observe the once-per-process rule.
func resetBypassLog() { bypassLogged = sync.Once{} }

// logQueueBypass records that a run went around the build queue. The bypass is
// a TOLERATED hole — any process can set APHROLLO_QUEUE=bypass with a target
// dir shaped like the mutation run's, and the shim will honour it. The harm is
// bounded to that one target dir, and what makes it tolerable is that every
// use is counted: `gate stats` shows it beside every other waiver.
func logQueueBypass(targetDir string) {
	bypassLogged.Do(func() {
		tdd.AppendGateLog("precommit", targetDir, "cargo", "queue-bypass", 0)
	})
}
