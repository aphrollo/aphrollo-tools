package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// refuseBareMutants reports whether this invocation is a hand-typed `cargo
// mutants`, and prints the one line that says what to do instead.
func refuseBareMutants(args []string, stderr io.Writer) bool {
	if cargoVerb(args) != "mutants" || os.Getenv(tdd.MutationGateEnv) == "1" {
		return false
	}
	fmt.Fprintln(stderr, mutantsRefusal)
	return true
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
